package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"go-rag/ingest"
	"go-rag/llm"
	"go-rag/rag"
	"go-rag/store"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

//go:embed templates/*.gohtml
var templatesFS embed.FS

const maxUploadBytes = 10 << 20

type Options struct {
	Addr             string
	SystemPromptFile string
	Title            string
	VectorStore      store.VectorStore
	DocumentStore    store.DocumentStore
	IngestDir        string
	ProcessedDir     string
	ImagesDir        string
}

type Server struct {
	text          llm.Chat
	vision        llm.Vision
	embedder      llm.Embedder
	retriever     rag.Retriever
	rewriter      rag.Rewriter
	vectorStore   store.VectorStore
	documentStore store.DocumentStore
	ingestDir     string
	processedDir  string
	imagesDir     string
	uploadMu      sync.Mutex
	uploads       map[string]map[chan uploadEvent]struct{}
	tpl           *template.Template
	system        string
	title         string
}

func New(text llm.Chat, vision llm.Vision, embedder llm.Embedder, retriever rag.Retriever, rewriter rag.Rewriter, opts Options) (*Server, error) {
	tpl, err := template.ParseFS(templatesFS, "templates/*.gohtml")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}

	title := opts.Title
	if title == "" {
		title = "RAG Chat"
	}

	return &Server{
		text:          text,
		vision:        vision,
		embedder:      embedder,
		retriever:     retriever,
		rewriter:      rewriter,
		vectorStore:   opts.VectorStore,
		documentStore: opts.DocumentStore,
		ingestDir:     opts.IngestDir,
		processedDir:  opts.ProcessedDir,
		imagesDir:     opts.ImagesDir,
		uploads:       make(map[string]map[chan uploadEvent]struct{}),
		tpl:           tpl,
		system:        readSystemPrompt(opts.SystemPromptFile),
		title:         title,
	}, nil
}

func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Get("/chat", s.handleChatPage)
	r.Post("/api/chat/stream", s.handleChatStream)
	r.Post("/api/upload", s.handleUpload)
	if s.imagesDir != "" {
		r.Post("/api/upload/image", s.handleUploadImage)
		fs := http.FileServer(http.Dir(s.imagesDir))
		r.Handle("/images/*", http.StripPrefix("/images", fs))
	}
	r.Get("/api/upload/events", s.handleUploadEvents)

	if s.vision.HasVision() {
		r.Post("/api/caption", s.handleCaption)
	}

	return r
}

func (s *Server) handleChatPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, "chat.gohtml", map[string]any{
		"Title":          s.title,
		"CaptionEnabled": s.vision != nil && s.vision.HasVision(),
	}); err != nil {
		log.Printf("[web] template error: %v", err)
	}
}

type captionResponse struct {
	Description string `json:"description"`
}

func (s *Server) handleCaption(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	if s.vision == nil || !s.vision.HasVision() {
		http.Error(w, "vision model is not configured", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, "image too large or malformed: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "missing 'image' field: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	if !ingest.IsImage(filepath.Base(header.Filename)) {
		http.Error(w, "unsupported image format", http.StatusUnsupportedMediaType)
		return
	}

	image, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read image: "+err.Error(), http.StatusBadRequest)
		return
	}
	mime := header.Header.Get("Content-Type")

	generationStarted := time.Now()
	description, err := s.vision.DescribeImage(r.Context(), mime, image)
	generationDuration := time.Since(generationStarted)
	if err != nil {
		log.Printf("[web] caption failed for %q after %s: %v", header.Filename, generationDuration, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Printf("[web] caption generation took %s; total request took %s", generationDuration, time.Since(started))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(captionResponse{Description: description})
}

type chatRequest struct {
	Messages []llm.Message `json:"messages"`
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json:"+err.Error(), http.StatusBadRequest)
		return
	}

	if len(req.Messages) == 0 {
		http.Error(w, "messages must not be empty", http.StatusBadRequest)
		return
	}

	if last := req.Messages[len(req.Messages)-1]; last.Role != "user" {
		http.Error(w, "last message must be from user", http.StatusBadRequest)
		return
	}

	history := req.Messages
	if s.system != "" {
		history = append([]llm.Message{{
			Role:    "system",
			Content: s.system,
		}}, history...)
	}

	turn := history
	var rewriteDuration time.Duration
	var retrieveDuration time.Duration
	if s.retriever != nil {
		question := req.Messages[len(req.Messages)-1].Content
		query := question
		if s.rewriter != nil {
			rewriteStarted := time.Now()
			rewritten, err := s.rewriter.Rewrite(r.Context(), history[:len(history)-1], question)
			rewriteDuration = time.Since(rewriteStarted)
			if err != nil {
				log.Printf("[web] rewrite error: %v; using original query", err)
			} else {
				query = rewritten
			}
		}
		retrieveStarted := time.Now()
		ctxText, err := s.retriever.Retrieve(r.Context(), query)
		retrieveDuration = time.Since(retrieveStarted)
		if err != nil {
			log.Printf("[web] retriebal error: %v", err)
		} else {
			turn = withInlineContext(history, ctxText)
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	send := func(event, data string) {
		if event != "" {
			fmt.Fprintf(w, "event: %s\n", event)
		}
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	modelStarted := time.Now()
	var firstToken time.Time
	_, err := s.text.ChatStream(r.Context(), turn, func(delta string) {
		if firstToken.IsZero() {
			firstToken = time.Now()
		}
		enc, _ := json.Marshal(delta)
		send("delta", string(enc))
	})
	modelDuration := time.Since(modelStarted)
	if err != nil {
		enc, _ := json.Marshal(err.Error())
		send("error", string(enc))
	} else {
		send("done", `""`)
	}
	if s.rewriter != nil {
		log.Printf("[web] chat rewrite took %s", rewriteDuration)
	}
	if s.retriever != nil {
		log.Printf("[web] chat retrieval took %s", retrieveDuration)
	}
	if firstToken.IsZero() {
		log.Printf("[web] chat first token was not received")
	} else {
		log.Printf("[web] chat first token after %s", firstToken.Sub(modelStarted))
	}
	log.Printf("[web] chat model stream took %s; total request took %s", modelDuration, time.Since(started))
}

type uploadResponse struct {
	Source string `json:"source"`
	Bytes  int    `json:"bytes"`
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if s.documentStore == nil || s.vectorStore == nil {
		http.Error(w, "ingest is not configured", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, "upload too large or malformed: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	name := filepath.Base(header.Filename)
	if !ingest.IsSupported(name) {
		http.Error(w, "unsupported format", http.StatusUnsupportedMediaType)
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read upload: "+err.Error(), http.StatusBadRequest)
		return
	}

	if s.ingestDir == "" {
		http.Error(w, "ingest directory is not configured", http.StatusServiceUnavailable)
		return
	}
	if err := os.MkdirAll(s.ingestDir, 0755); err != nil {
		http.Error(w, "create ingest directory: "+err.Error(), http.StatusInternalServerError)
		return
	}
	source := filepath.Join(s.ingestDir, name)
	if err := os.WriteFile(source, content, 0644); err != nil {
		http.Error(w, "save upload: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(uploadResponse{
		Source: name,
		Bytes:  len(content),
	})
}

type uploadImageResponse struct {
	Source      string `json:"source"`
	ImagePath   string `json:"image_path"`
	Description string `json:"description"`
	Bytes       int    `json:"bytes"`
	Chunks      int    `json:"chunks"`
}

func (s *Server) handleUploadImage(w http.ResponseWriter, r *http.Request) {
	if s.documentStore == nil || s.vectorStore == nil {
		http.Error(w, "ingest is not configured", http.StatusServiceUnavailable)
		return
	}

	if s.imagesDir == "" {
		http.Error(w, "image upload not configured", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		http.Error(w, "upload too large or malformed: "+err.Error(), http.StatusBadRequest)
		return
	}

	description := strings.TrimSpace(r.FormValue("description"))
	if description == "" {
		http.Error(w, "description is required", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "missing 'image' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	original := filepath.Base(header.Filename)
	if !ingest.IsImage(original) {
		http.Error(w, "unsupported image format (allowed: .png, .jpg, .jpeg, .webp, .gif)", http.StatusUnsupportedMediaType)
		return
	}

	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(s.imagesDir, 0755); err != nil {
		http.Error(w, "create image directory: "+err.Error(), http.StatusInternalServerError)
		return
	}
	path := filepath.Join(s.imagesDir, original)
	if err := os.WriteFile(path, content, 0644); err != nil {
		http.Error(w, "save image: "+err.Error(), http.StatusInternalServerError)
		return
	}

	chunks, err := ingest.ProcessImage(r.Context(), original, description, ingest.Options{}, s.embedder, s.documentStore, s.vectorStore)
	if err != nil {
		_ = os.Remove(path)
		http.Error(w, "ingest image: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(uploadImageResponse{
		Source:      original,
		ImagePath:   ingest.ImagePathPrefix + original,
		Description: description,
		Bytes:       len(content),
		Chunks:      chunks,
	})
}

type uploadEvent struct {
	Chunks int    `json:"chunks"`
	Err    string `json:"error,omitempty"`
}

func (s *Server) handleUploadEvents(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(strings.TrimSpace(r.URL.Query().Get("source")))
	if name == "." || name == "" || !ingest.IsSupported(name) {
		http.Error(w, "invalid source", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	events, unsubscribe := s.subscribeUpload(name)
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	select {
	case <-r.Context().Done():
	case event := <-events:
		if event.Err != "" {
			writeSSE(w, flusher, "error", event.Err)
			return
		}
		data, _ := json.Marshal(event)
		writeSSE(w, flusher, "complete", string(data))
	}
}

func (s *Server) subscribeUpload(source string) (<-chan uploadEvent, func()) {
	channel := make(chan uploadEvent, 1)
	s.uploadMu.Lock()
	if s.uploads[source] == nil {
		s.uploads[source] = make(map[chan uploadEvent]struct{})
	}
	s.uploads[source][channel] = struct{}{}
	s.uploadMu.Unlock()
	return channel, func() {
		s.uploadMu.Lock()
		delete(s.uploads[source], channel)
		if len(s.uploads[source]) == 0 {
			delete(s.uploads, source)
		}
		s.uploadMu.Unlock()
	}
}

func (s *Server) NotifyUpload(path string, chunks int, err error) {
	source := filepath.Base(path)
	event := uploadEvent{Chunks: chunks}
	if err != nil {
		event.Err = err.Error()
	}
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	for channel := range s.uploads[source] {
		channel <- event
		close(channel)
	}
	delete(s.uploads, source)
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event, data string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
	flusher.Flush()
}

func withInlineContext(history []llm.Message, contextText string) []llm.Message {
	if len(history) == 0 || contextText == "" {
		return history
	}
	last := history[len(history)-1]
	if last.Role != "user" {
		return history
	}
	out := make([]llm.Message, len(history))
	copy(out, history)
	out[len(out)-1] = llm.Message{
		Role:    "user",
		Content: contextText + "\n\n--- Question ---\n\n" + last.Content,
	}
	return out
}

func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errChan := make(chan error, 1)
	go func() {
		defer close(errChan)
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return nil

	case err, ok := <-errChan:
		if !ok {
			return nil
		}
		return err
	}
}

func readSystemPrompt(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ""
	}
	return strings.TrimSpace(string(data))
}
