package rag

import (
	"context"
	"fmt"
	"go-rag/llm"
	"go-rag/store"
)

type Retriever interface {
	Retrieve(ctx context.Context, question string) (string, error)
}

type retriever struct {
	embedder  llm.QueryEmbedder
	documents store.DocumentStore
	vectors   store.VectorStore
	topK      int
}

func NewRetriever(embedder llm.QueryEmbedder, documents store.DocumentStore, vectors store.VectorStore, topK int) Retriever {
	if topK <= 0 {
		topK = 5
	}
	return &retriever{embedder: embedder, documents: documents, vectors: vectors, topK: topK}
}

func (r *retriever) Retrieve(ctx context.Context, question string) (string, error) {
	embedding, err := r.embedder.EmbedQuery(ctx, question)
	if err != nil {
		return "", fmt.Errorf("embed question: %w", err)
	}
	hits, err := r.vectors.Search(ctx, embedding, r.topK)
	if err != nil {
		return "", fmt.Errorf("search vectors: %w", err)
	}
	if len(hits) == 0 {
		return "", nil
	}
	ids := make([]string, 0, len(hits))
	for _, hit := range hits {
		ids = append(ids, hit.ID)
	}
	docs, err := r.documents.Get(ctx, ids...)
	if err != nil {
		return "", fmt.Errorf("get documents: %w", err)
	}
	byID := make(map[string]store.Document, len(docs))
	for _, doc := range docs {
		byID[doc.ID] = doc
	}
	return formatContext(hits, byID), nil
}
