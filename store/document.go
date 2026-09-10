package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	cfclient "go-rag/cloudflare"

	cloudflareapi "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/d1"
	"github.com/cloudflare/cloudflare-go/v7/packages/pagination"
)

type DocumentStore interface {
	Upsert(ctx context.Context, docs []Document) error
	Get(ctx context.Context, ids ...string) ([]Document, error)
	GetBySource(ctx context.Context, source string) ([]Document, error)
	Delete(ctx context.Context, ids ...string) error
	DeleteBySource(ctx context.Context, source string) error
}

type CloudflareDocumentStore struct {
	client *cfclient.Client
}

type Document struct {
	ID       string            `json:"id"`
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata"`
}

func NewDocumentStore(client *cfclient.Client) *CloudflareDocumentStore {
	return &CloudflareDocumentStore{
		client: client,
	}
}

func (s *CloudflareDocumentStore) Upsert(ctx context.Context, docs []Document) error {
	if len(docs) == 0 {
		return errors.New("at least one document is required")
	}

	params := make([][]string, 0, len(docs))
	for _, doc := range docs {
		if doc.ID == "" {
			return errors.New("document ID is required")
		}

		metadata, err := json.Marshal(doc.Metadata)
		if err != nil {
			return fmt.Errorf("marshal metadata for document %q: %w", doc.ID, err)
		}

		params = append(params, []string{doc.ID, doc.Content, string(metadata)})
	}

	return s.queryBatch(ctx, "INSERT INTO documents (id, content, metadata) VALUES (?, ?, ?) ON CONFLICT(id) DO UPDATE SET content = excluded.content, metadata = excluded.metadata", params)
}

func (s *CloudflareDocumentStore) Get(ctx context.Context, ids ...string) ([]Document, error) {
	if len(ids) == 0 {
		return nil, errors.New("at least one document ID is required")
	}

	placeholders := makePlaceholders(len(ids))
	result, err := s.query(ctx,
		fmt.Sprintf("SELECT id, content, metadata FROM documents WHERE id IN (%s)", placeholders),
		ids,
	)
	if err != nil {
		return nil, err
	}

	documents := make([]Document, 0, len(result.Results))
	for _, row := range result.Results {
		document, err := decodeDocument(row)
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, nil
}

func (s *CloudflareDocumentStore) GetBySource(ctx context.Context, source string) ([]Document, error) {
	result, err := s.query(ctx,
		"SELECT id, content, metadata FROM documents WHERE json_extract(metadata, '$.source') = ?",
		[]string{source},
	)
	if err != nil {
		return nil, err
	}

	documents := make([]Document, 0, len(result.Results))
	for _, row := range result.Results {
		document, err := decodeDocument(row)
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	return documents, nil
}

func (s *CloudflareDocumentStore) Delete(ctx context.Context, ids ...string) error {
	if len(ids) == 0 {
		return errors.New("at least one document ID is required")
	}

	_, err := s.query(ctx,
		fmt.Sprintf("DELETE FROM documents WHERE id IN (%s)", makePlaceholders(len(ids))),
		ids,
	)
	return err
}

func (s *CloudflareDocumentStore) DeleteBySource(ctx context.Context, source string) error {
	_, err := s.query(ctx, "DELETE FROM documents WHERE json_extract(metadata, '$.source') = ?", []string{source})
	return err
}

func (s *CloudflareDocumentStore) query(ctx context.Context, sql string, params []string) (*d1.QueryResult, error) {
	result, err := s.execute(ctx, d1.DatabaseQueryParamsBodyD1SingleQuery{
		Sql:    cloudflareapi.F(sql),
		Params: cloudflareapi.F(params),
	})
	if err != nil {
		return nil, err
	}
	if len(result.Result) == 0 {
		return nil, errors.New("D1 returned no query result")
	}
	return &result.Result[0], nil
}

func (s *CloudflareDocumentStore) queryBatch(ctx context.Context, sql string, params [][]string) error {
	queries := make([]d1.DatabaseQueryParamsBodyMultipleQueriesBatch, 0, len(params))
	for _, values := range params {
		queries = append(queries, d1.DatabaseQueryParamsBodyMultipleQueriesBatch{
			Sql:    cloudflareapi.F(sql),
			Params: cloudflareapi.F(values),
		})
	}

	_, err := s.execute(ctx, d1.DatabaseQueryParamsBodyMultipleQueries{
		Batch: cloudflareapi.F(queries),
	})
	return err
}

func (s *CloudflareDocumentStore) execute(ctx context.Context, body d1.DatabaseQueryParamsBodyUnion) (*pagination.SinglePage[d1.QueryResult], error) {
	result, err := s.client.D1.Query(ctx, s.client.D1DatabaseID, d1.DatabaseQueryParams{
		AccountID: cloudflareapi.F(s.client.AccountID),
		Body:      body,
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func decodeDocument(row interface{}) (Document, error) {
	values, ok := row.(map[string]interface{})
	if !ok {
		return Document{}, fmt.Errorf("unexpected D1 document row type %T", row)
	}

	id, ok := values["id"].(string)
	if !ok {
		return Document{}, fmt.Errorf("unexpected D1 document ID type %T", values["id"])
	}
	content, ok := values["content"].(string)
	if !ok {
		return Document{}, fmt.Errorf("unexpected D1 document content type %T", values["content"])
	}

	metadata := map[string]string{}
	if rawMetadata, ok := values["metadata"].(string); ok && rawMetadata != "" {
		if err := json.Unmarshal([]byte(rawMetadata), &metadata); err != nil {
			return Document{}, fmt.Errorf("decode metadata for document %q: %w", id, err)
		}
	}

	return Document{ID: id, Content: content, Metadata: metadata}, nil
}

func makePlaceholders(count int) string {
	placeholders := "?"
	for i := 1; i < count; i++ {
		placeholders += ", ?"
	}
	return placeholders
}

var _ DocumentStore = (*CloudflareDocumentStore)(nil)
