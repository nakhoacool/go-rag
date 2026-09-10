package app

import (
	"context"
	"go-rag/chat"
	"go-rag/cloudflare"
	"go-rag/config"
	"go-rag/document"
	"go-rag/ingest"
	"go-rag/llm"
	"go-rag/rag"
	"go-rag/vector"
	"log"
	"sync"
)

func Run(parent context.Context, cfg config.Config) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	client := llm.New(cfg)
	cloudflareClient := cloudflare.New(cfg)
	documents := document.NewDocumentStore(cloudflareClient)
	vectors := vector.NewVectorStore(cloudflareClient)
	embedder := llm.NewJinaEmbedder(cfg)
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := ingest.Watch(
			ctx,
			ingest.Options{SourceDir: cfg.IngestDir, ProcessedDir: cfg.ProcessedDir},
			embedder,
			documents,
			vectors,
			log.Default(),
		); err != nil {
			log.Printf("ingest watcher stopped: %v", err)
		}
	})
	log.Printf("Watching directory %s for new documents to ingest...", cfg.IngestDir)

	err := chat.RunREPL(ctx, client, chat.Options{
		SystemPromptFile: cfg.SystemPromptFile,
		Retriever:        rag.NewRetriever(embedder, documents, vectors, 5),
		Rewriter:         rag.NewRewriter(client),
	})
	cancel()
	wg.Wait()
	return err
}
