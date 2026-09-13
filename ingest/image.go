package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go-rag/llm"
	"go-rag/store"
	"path/filepath"
	"strings"
	"time"
)

const ImagePathPrefix = "/images/"

var imageExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".webp": true,
	".gif":  true,
}

func IsImage(name string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(name))]
}

func processImage(ctx context.Context, name, description string, opts Options, embedder llm.Embedder, documents store.DocumentStore, vectors store.VectorStore) (int, error) {
	if embedder == nil {
		return 0, errors.New("embedder is required")
	}
	if documents == nil {
		return 0, errors.New("document store is required")
	}
	if vectors == nil {
		return 0, errors.New("vector store is required")
	}

	base := filepath.Base(name)
	if !IsImage(base) {
		return 0, errors.New("unsupported image format")
	}

	desc := strings.TrimSpace(description)
	if desc == "" {
		return 0, errors.New("description is required")
	}

	size := opts.ChunkSize
	if size <= 0 {
		size = defaultChunkSize
	}

	overlap := opts.ChunkOverlap
	if overlap < 0 {
		overlap = defaultChunkOverlap
	}

	chunks := chunk(desc, size, overlap)
	if len(chunks) == 0 {
		return 0, errors.New("no chunks produced")
	}

	embeddings, err := embedder.Embed(ctx, chunks)
	if err != nil {
		return 0, fmt.Errorf("embed: %w", err)
	}
	if len(embeddings) != len(chunks) {
		return 0, fmt.Errorf("embed: got %d vectors for %d chunks", len(embeddings), len(chunks))
	}

	source := ImagePathPrefix + base
	docs := make([]store.Document, len(chunks))
	ids := make([]string, len(chunks))
	for index, content := range chunks {
		id := fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "%s:%d", source, index)))
		metadata := map[string]string{
			"source":          source,
			"source_relative": source,
			"type":            "image",
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
	if err := removeStale(ctx, source, ids, documents, vectors); err != nil {
		return 0, err
	}
	return len(chunks), nil
}
