package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	cfclient "go-rag/cloudflare"

	cloudflareapi "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/option"
	"github.com/cloudflare/cloudflare-go/v7/vectorize"
)

type VectorStore interface {
	Upsert(ctx context.Context, id string, embedding []float64, metadata map[string]string) error
	Delete(ctx context.Context, ids ...string) error
	Search(ctx context.Context, embedding []float64, topK int) ([]Match, error)
}

type CloudflareVectorStore struct {
	client *cfclient.Client
}

type VectorRecord struct {
	ID       string            `json:"id"`
	Values   []float64         `json:"values"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type Match struct {
	ID       string
	Score    float64
	Metadata map[string]string
}

func NewVectorStore(client *cfclient.Client) *CloudflareVectorStore {
	return &CloudflareVectorStore{
		client: client,
	}
}

func (v *CloudflareVectorStore) Upsert(ctx context.Context, id string, embedding []float64, metadata map[string]string) error {
	vector, err := json.Marshal(VectorRecord{
		ID:       id,
		Values:   embedding,
		Metadata: metadata,
	})
	if err != nil {
		return err
	}

	vector = append(vector, '\n')
	var response vectorize.IndexUpsertResponseEnvelope
	err = v.client.CF.Post(
		ctx,
		"accounts/"+v.client.AccountID+"/vectorize/v2/indexes/"+v.client.VectorizeIndex+"/upsert",
		nil,
		&response,
		option.WithRequestBody("application/x-ndjson", bytes.NewReader(vector)),
	)
	return err
}

func (v *CloudflareVectorStore) Delete(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return errors.New("at least one vector ID is required")
	}

	_, err := v.client.Vectorize.DeleteByIDs(
		ctx,
		v.client.VectorizeIndex,
		vectorize.IndexDeleteByIDsParams{
			AccountID: cloudflareapi.F(v.client.AccountID),
			IDs:       cloudflareapi.F(ids),
		},
	)
	return err
}

func (v *CloudflareVectorStore) Search(ctx context.Context, embedding []float64, topK int) ([]Match, error) {
	if len(embedding) == 0 {
		return nil, errors.New("embedding is required")
	}
	if topK <= 0 {
		return nil, errors.New("topK must be greater than zero")
	}

	result, err := v.client.Vectorize.Query(
		ctx,
		v.client.VectorizeIndex,
		vectorize.IndexQueryParams{
			AccountID:      cloudflareapi.F(v.client.AccountID),
			Vector:         cloudflareapi.F(embedding),
			TopK:           cloudflareapi.F(float64(topK)),
			ReturnMetadata: cloudflareapi.F(vectorize.IndexQueryParamsReturnMetadataAll),
		},
	)
	if err != nil {
		return nil, err
	}

	matches := make([]Match, 0, len(result.Matches))
	for _, match := range result.Matches {
		metadata := map[string]string{}
		if match.Metadata != nil {
			rawMetadata, err := json.Marshal(match.Metadata)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(rawMetadata, &metadata); err != nil {
				return nil, err
			}
		}
		matches = append(matches, Match{ID: match.ID, Score: match.Score, Metadata: metadata})
	}
	return matches, nil
}

var _ VectorStore = (*CloudflareVectorStore)(nil)
