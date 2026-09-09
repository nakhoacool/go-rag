package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go-rag/document"
	"go-rag/llm"
	"go-rag/vector"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultChunkSize    = 1000
	defaultChunkOverlap = 100
)

type Options struct {
	SourceDir    string
	ProcessedDir string
	ChunkSize    int
	ChunkOverlap int
}

func processContent(ctx context.Context, source string, content []byte, opts Options, embedder llm.Embedder, documents document.DocumentStore, vectors vector.VectorStore) (int, error) {
	if documents == nil {
		return 0, errors.New("document store is required")
	}
	if vectors == nil {
		return 0, errors.New("vector store is required")
	}
	if embedder == nil {
		return 0, errors.New("embedder is required")
	}

	base := filepath.Base(source)
	if !supportedFormat(base) {
		return 0, fmt.Errorf("unsupported format: %s", filepath.Ext(base))
	}

	size := opts.ChunkSize
	if size <= 0 {
		size = defaultChunkSize
	}

	overlap := opts.ChunkOverlap
	if overlap <= 0 {
		overlap = defaultChunkOverlap
	}

	text := strings.TrimSpace(string(content))
	if text == "" {
		return 0, errors.New("file is empty")
	}

	chunks := chunk(text, size, overlap)
	if len(chunks) == 0 {
		return 0, errors.New("no chunk produced")
	}

	embeddings, err := embedder.Embed(ctx, chunks)
	if err != nil {
		return 0, fmt.Errorf("embed: %w", err)
	}

	if len(embeddings) != len(chunks) {
		return 0, fmt.Errorf("embed got %d vectors for %d chunks", len(embeddings), len(chunks))
	}

	docs := make([]document.Document, len(chunks))
	ids := make([]string, len(chunks))
	for index, content := range chunks {
		id := fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "%s:%d", source, index)))
		metadata := map[string]string{
			"source":       source,
			"chunk_index":  fmt.Sprintf("%d", index),
			"chunks_total": fmt.Sprintf("%d", len(chunks)),
			"ingested_at":  time.Now().UTC().Format(time.RFC3339),
		}
		ids[index] = id
		docs[index] = document.Document{ID: id, Content: content, Metadata: metadata}
	}
	if err := documents.Upsert(ctx, docs); err != nil {
		return 0, fmt.Errorf("store documents: %w", err)
	}
	for index, embedding := range embeddings {
		if err := vectors.Upsert(ctx, ids[index], embedding, docs[index].Metadata); err != nil {
			return index, fmt.Errorf("store vector %q: %w", ids[index], err)
		}
	}
	if err := writeChunks(opts, source, chunks); err != nil {
		return 0, err
	}
	return len(chunks), nil
}

func writeChunks(opts Options, source string, chunks []string) error {
	relative, err := filepath.Rel(opts.SourceDir, source)
	if err != nil {
		return fmt.Errorf("get relative path for %q: %w", source, err)
	}

	directory := filepath.Join(opts.ProcessedDir, filepath.Dir(relative), strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative)))
	if err := os.MkdirAll(directory, 0755); err != nil {
		return fmt.Errorf("create processed directory: %w", err)
	}

	extension := filepath.Ext(relative)
	for index, content := range chunks {
		name := fmt.Sprintf("chunk-%03d%s", index+1, extension)
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write chunk %q: %w", path, err)
		}
	}
	return nil
}

func supportedFormat(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md":
		return true
	}
	return false
}
