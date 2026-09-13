package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go-rag/llm"
	"go-rag/store"
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
	SourceDir       string
	ProcessedDir    string
	ProcessExisting bool
	ChunkSize       int
	ChunkOverlap    int
	OnProcessed     func(path string, chunks int, err error)
}

func processContent(ctx context.Context, source string, content []byte, opts Options, embedder llm.Embedder, documents store.DocumentStore, vectors store.VectorStore) (int, error) {
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
	if overlap < 0 {
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
	sourceKey, err := sourcePath(opts.SourceDir, source)
	if err != nil {
		return 0, err
	}

	embeddings, err := embedder.Embed(ctx, chunks)
	if err != nil {
		return 0, fmt.Errorf("embed: %w", err)
	}

	if len(embeddings) != len(chunks) {
		return 0, fmt.Errorf("embed got %d vectors for %d chunks", len(embeddings), len(chunks))
	}

	docs := make([]store.Document, len(chunks))
	ids := make([]string, len(chunks))
	for index, content := range chunks {
		id := fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "%s:%d", source, index)))
		metadata := map[string]string{
			"source":          sourceKey,
			"source_relative": sourceKey,
			"chunk_index":     fmt.Sprintf("%d", index),
			"chunks_total":    fmt.Sprintf("%d", len(chunks)),
			"ingested_at":     time.Now().UTC().Format(time.RFC3339),
		}
		ids[index] = id
		docs[index] = store.Document{ID: id, Content: content, Metadata: metadata}
	}
	if err := documents.Upsert(ctx, docs); err != nil {
		return 0, fmt.Errorf("store documents: %w", err)
	}
	for index, embedding := range embeddings {
		if err := vectors.Upsert(ctx, ids[index], embedding, docs[index].Metadata); err != nil {
			return index, fmt.Errorf("store vector %q: %w", ids[index], err)
		}
	}
	if err := removeStale(ctx, sourceKey, ids, documents, vectors); err != nil {
		return 0, err
	}
	if err := writeChunks(opts, source, chunks); err != nil {
		return 0, err
	}
	return len(chunks), nil
}

func sourcePath(sourceDir, source string) (string, error) {
	relative, err := filepath.Rel(sourceDir, source)
	if err != nil {
		return "", fmt.Errorf("get relative path for %q: %w", source, err)
	}
	return filepath.Clean(relative), nil
}

func removeStale(ctx context.Context, source string, keep []string, documents store.DocumentStore, vectors store.VectorStore) error {
	existing, err := documents.GetBySource(ctx, source)
	if err != nil {
		return fmt.Errorf("find existing documents: %w", err)
	}
	current := make(map[string]struct{}, len(keep))
	for _, id := range keep {
		current[id] = struct{}{}
	}
	stale := make([]string, 0, len(existing))
	for _, document := range existing {
		if _, ok := current[document.ID]; !ok {
			stale = append(stale, document.ID)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	if err := vectors.Delete(ctx, stale...); err != nil {
		return fmt.Errorf("delete stale vectors: %w", err)
	}
	if err := documents.Delete(ctx, stale...); err != nil {
		return fmt.Errorf("delete stale documents: %w", err)
	}
	return nil
}

func removeSource(ctx context.Context, source string, documents store.DocumentStore, vectors store.VectorStore) error {
	existing, err := documents.GetBySource(ctx, source)
	if err != nil {
		return fmt.Errorf("find source documents: %w", err)
	}
	ids := make([]string, 0, len(existing))
	for _, document := range existing {
		ids = append(ids, document.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := vectors.Delete(ctx, ids...); err != nil {
		return fmt.Errorf("delete source vectors: %w", err)
	}
	if err := documents.Delete(ctx, ids...); err != nil {
		return fmt.Errorf("delete source documents: %w", err)
	}
	return nil
}

func removeProcessed(opts Options, source string) error {
	relative, err := filepath.Rel(opts.SourceDir, source)
	if err != nil {
		return fmt.Errorf("get relative path for %q: %w", source, err)
	}
	directory := filepath.Join(opts.ProcessedDir, filepath.Dir(relative), strings.TrimSuffix(filepath.Base(relative), filepath.Ext(relative)))
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("remove processed directory: %w", err)
	}
	return nil
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
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read processed directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "chunk-") {
			continue
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
			return fmt.Errorf("remove stale chunk %q: %w", entry.Name(), err)
		}
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

func IsSupported(path string) bool {
	return supportedFormat(path)
}

func supportedFormat(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".txt", ".md":
		return true
	}
	return false
}
