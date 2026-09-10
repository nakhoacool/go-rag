package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go-rag/config"
	"io"
	"net/http"
)

type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

type QueryEmbedder interface {
	EmbedQuery(ctx context.Context, text string) ([]float64, error)
}

type JinaEmbedder struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

type jinaEmbeddingRequest struct {
	Model      string              `json:"model"`
	Task       string              `json:"task"`
	Normalized bool                `json:"normalized"`
	Input      []map[string]string `json:"input"`
}

type jinaEmbeddingResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

func NewJinaEmbedder(cfg config.Config) *JinaEmbedder {
	return &JinaEmbedder{
		baseURL: cfg.JinaEmbeddingBaseURL,
		apiKey:  cfg.JinaAPIKey,
		model:   cfg.JinaEmbeddingModel,
		client:  http.DefaultClient,
	}
}

func (e *JinaEmbedder) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	return e.embed(ctx, texts, "retrieval.passage")
}

func (e *JinaEmbedder) EmbedQuery(ctx context.Context, text string) ([]float64, error) {
	embeddings, err := e.embed(ctx, []string{text}, "retrieval.query")
	if err != nil {
		return nil, err
	}
	return embeddings[0], nil
}

func (e *JinaEmbedder) embed(ctx context.Context, texts []string, task string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, fmt.Errorf("at least one text is required")
	}
	if e.apiKey == "" {
		return nil, fmt.Errorf("JINA_API_KEY is required")
	}
	if e.model == "" {
		return nil, fmt.Errorf("JINA_EMBEDDING_MODEL is required")
	}
	if e.baseURL == "" {
		return nil, fmt.Errorf("JINA_EMBEDDING_BASE_URL is required")
	}

	body, err := json.Marshal(jinaEmbeddingRequest{
		Model:      e.model,
		Task:       task,
		Normalized: true,
		Input: func() []map[string]string {
			input := make([]map[string]string, len(texts))
			for index, text := range texts {
				input[index] = map[string]string{"text": text}
			}
			return input
		}(),
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("Jina embeddings request failed: %s: %s", resp.Status, bytes.TrimSpace(message))
	}

	var result jinaEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode Jina embeddings response: %w", err)
	}
	if len(result.Data) != len(texts) {
		return nil, fmt.Errorf("Jina returned %d embeddings for %d texts", len(result.Data), len(texts))
	}

	embeddings := make([][]float64, len(texts))
	for _, item := range result.Data {
		if item.Index < 0 || item.Index >= len(texts) {
			return nil, fmt.Errorf("Jina returned invalid embedding index %d", item.Index)
		}
		embeddings[item.Index] = item.Embedding
	}
	for index, embedding := range embeddings {
		if len(embedding) == 0 {
			return nil, fmt.Errorf("Jina returned no embedding for text %d", index)
		}
	}
	return embeddings, nil
}

var _ Embedder = (*JinaEmbedder)(nil)
var _ QueryEmbedder = (*JinaEmbedder)(nil)
