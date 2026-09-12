package ingest

import (
	"context"
	"errors"
	"fmt"
	"go-rag/llm"
	"go-rag/store"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const debounceDelay = 500 * time.Millisecond

func Watch(ctx context.Context, opts Options, embedder llm.Embedder, documents store.DocumentStore, vectors store.VectorStore, logger *log.Logger) error {
	if filepath.Clean(opts.SourceDir) == filepath.Clean(opts.ProcessedDir) {
		return errors.New("source and processed directories must differ")
	}
	if err := os.MkdirAll(opts.SourceDir, 0755); err != nil {
		return fmt.Errorf("create source dir: %w", err)
	}
	if err := os.MkdirAll(opts.ProcessedDir, 0755); err != nil {
		return fmt.Errorf("create processed dir: %w", err)
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer w.Close()

	var initialFiles []string
	if err := filepath.WalkDir(opts.SourceDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return w.Add(path)
		}
		if supportedFormat(path) {
			initialFiles = append(initialFiles, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("watch source dirs: %w", err)
	}

	if logger == nil {
		logger = log.Default()
	}

	jobs := make(chan string)
	timers := map[string]*time.Timer{}
	schedule := func(path string) {
		if !supportedFormat(path) {
			return
		}
		if timer := timers[path]; timer != nil {
			timer.Stop()
		}
		timers[path] = time.AfterFunc(debounceDelay, func() {
			select {
			case jobs <- path:
			case <-ctx.Done():
			}
		})
	}

	if opts.ProcessExisting {
		logger.Printf("initial files to be processed: %v", initialFiles)
		for _, path := range initialFiles {
			schedule(path)
		}
	}

	for {
		select {
		case <-ctx.Done():
			for _, timer := range timers {
				timer.Stop()
			}
			return nil
		case path := <-jobs:
			delete(timers, path)
			content, err := os.ReadFile(path)
			if err != nil {
				if !os.IsNotExist(err) {
					logger.Printf("read %q: %v", path, err)
				}
				continue
			}
			count, err := processContent(ctx, path, content, opts, embedder, documents, vectors)
			if opts.OnProcessed != nil {
				opts.OnProcessed(path, count, err)
			}
			if err != nil {
				logger.Printf("ingest %q: %v", path, err)
				continue
			}
			logger.Printf("ingested %d chunks from %s", count, path)
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			logger.Printf("watcher error: %v", err)
		case event, ok := <-w.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 && supportedFormat(event.Name) {
				source, err := sourcePath(opts.SourceDir, event.Name)
				if err != nil {
					logger.Printf("source path %q: %v", event.Name, err)
				} else if err := removeSource(ctx, source, documents, vectors); err != nil {
					logger.Printf("remove source %q: %v", event.Name, err)
				}
				if err := removeProcessed(opts, event.Name); err != nil {
					logger.Printf("remove processed %q: %v", event.Name, err)
				}
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if err := filepath.WalkDir(event.Name, func(path string, entry os.DirEntry, err error) error {
						if err != nil {
							return err
						}
						if entry.IsDir() {
							return w.Add(path)
						}
						schedule(path)
						return nil
					}); err != nil {
						logger.Printf("watch %q: %v", event.Name, err)
					}
					continue
				}
			}
			if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				schedule(strings.TrimSpace(event.Name))
			}
		}
	}
}
