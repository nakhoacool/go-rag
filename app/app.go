package app

import (
	"bufio"
	"context"
	"fmt"
	"go-rag/chat"
	"go-rag/cloudflare"
	"go-rag/config"
	"go-rag/ingest"
	"go-rag/llm"
	"go-rag/rag"
	"go-rag/store"
	"go-rag/web"
	"log"
	"os"
	"sync"
	"syscall"
)

func Run(parent context.Context, cfg config.Config) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	processExisting := askProcessExisting()

	client := llm.New(cfg)
	cloudflareClient := cloudflare.New(cfg)
	documents := store.NewDocumentStore(cloudflareClient)
	vectors := store.NewVectorStore(cloudflareClient)
	embedder := llm.NewJinaEmbedder(cfg)
	retriever := rag.NewRetriever(embedder, documents, vectors, 5)
	rewriter := rag.NewRewriter(client)

	var wg sync.WaitGroup
	wg.Go(func() {
		if err := ingest.Watch(
			ctx,
			ingest.Options{SourceDir: cfg.IngestDir, ProcessedDir: cfg.ProcessedDir, ProcessExisting: processExisting},
			embedder,
			documents,
			vectors,
			log.Default(),
		); err != nil {
			log.Printf("ingest watcher stopped: %v", err)
		}
	})
	log.Printf("Watching directory %s for new documents to ingest...", cfg.IngestDir)

	if cfg.HTTPAddr != "" {
		srv, err := web.New(client, embedder, retriever, rewriter, web.Options{
			Addr:             cfg.HTTPAddr,
			SystemPromptFile: cfg.SystemPromptFile,
			VectorStore:      vectors,
			DocumentStore:    documents,
			ProcessedDir:     cfg.ProcessedDir,
			ImagesDir:        cfg.ImagesDir,
		})
		if err != nil {
			return err
		}
		wg.Go(func() {
			if err := srv.Run(ctx, cfg.HTTPAddr); err != nil && ctx.Err() == nil {
				log.Printf("web server stopped: %v", err)
			}
		})
		log.Printf("web chat at http://localhost%s/chat", cfg.HTTPAddr)
	}

	err := chat.RunREPL(ctx, client, chat.Options{
		SystemPromptFile: cfg.SystemPromptFile,
		Retriever:        retriever,
		Rewriter:         rewriter,
	})
	cancel()
	wg.Wait()
	return err
}

func askProcessExisting() bool {
	fmt.Print("Process existing files now? [y/N] (5s): ")
	fd := int(os.Stdin.Fd())
	set := &syscall.FdSet{}
	set.Bits[fd/64] |= 1 << (uint(fd) % 64)
	ready, err := syscall.Select(fd+1, set, nil, nil, &syscall.Timeval{Sec: 5})
	if err != nil || ready == 0 {
		fmt.Println()
		return false
	}

	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Println()
	return err == nil && (answer == "y\n" || answer == "Y\n")
}
