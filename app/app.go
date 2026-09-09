package app

import (
	"context"
	"go-rag/chat"
	"go-rag/cloudflare"
	"go-rag/config"
	"go-rag/document"
	"go-rag/ingest"
	"go-rag/llm"
	"go-rag/vector"
	"log"
	"sync"
)

func Run(parent context.Context, cfg config.Config) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	client := llm.New(cfg)
	cloudflareClient := cloudflare.New(cfg)
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := ingest.Watch(
			ctx,
			ingest.Options{SourceDir: cfg.IngestDir, ProcessedDir: cfg.ProcessedDir},
			llm.NewJinaEmbedder(cfg),
			document.NewDocumentStore(cloudflareClient),
			vector.NewVectorStore(cloudflareClient),
			log.Default(),
		); err != nil {
			log.Printf("ingest watcher stopped: %v", err)
		}
	})
	log.Printf("Watching directory %s for new documents to ingest...", cfg.IngestDir)

	err := chat.RunREPL(ctx, client, chat.Options{
		SystemPromptFile: cfg.SystemPromptFile,
	})
	cancel()
	wg.Wait()
	return err
}
