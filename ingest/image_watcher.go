package ingest

import (
	"context"
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

// WatchImages watches ImagesDir for new images and processes them via processImage.
// It is intentionally separate from Watch (documents) so each can be tuned/reasoned about independently.
func WatchImages(ctx context.Context, opts Options, embedder llm.Embedder, documents store.DocumentStore, vectors store.VectorStore, logger *log.Logger) error {
	if opts.ImagesDir == "" {
		return nil
	}
	if err := os.MkdirAll(opts.ImagesDir, 0755); err != nil {
		return fmt.Errorf("create images dir: %w", err)
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create image watcher: %w", err)
	}
	defer w.Close()

	var initialFiles []string
	if err := filepath.WalkDir(opts.ImagesDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return w.Add(path)
		}
		if IsImage(path) && !strings.HasSuffix(path, ".description") {
			initialFiles = append(initialFiles, path)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("watch images dirs: %w", err)
	}

	if logger == nil {
		logger = log.Default()
	}

	jobs := make(chan string)
	timers := map[string]*time.Timer{}
	schedule := func(path string) {
		if strings.HasSuffix(path, ".description") {
			return
		}
		if !IsImage(path) {
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
		logger.Printf("initial images to be processed: %v", initialFiles)
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
			descPath := path + ".description"
			descBytes, err := os.ReadFile(descPath)
			if err != nil {
				if !os.IsNotExist(err) {
					logger.Printf("read description %q: %v", descPath, err)
				} else {
					logger.Printf("missing description for image %q (expected %q)", path, descPath)
				}
				if opts.OnProcessed != nil {
					opts.OnProcessed(path, 0, fmt.Errorf("missing description: %w", err))
				}
				continue
			}
			desc := strings.TrimSpace(string(descBytes))
			count, err := processImage(ctx, filepath.Base(path), desc, opts, embedder, documents, vectors)
			if opts.OnProcessed != nil {
				opts.OnProcessed(path, count, err)
			}
			if err != nil {
				logger.Printf("ingest image %q: %v", path, err)
				continue
			}
			logger.Printf("ingested %d chunks from image %s", count, path)
			if err := os.Remove(descPath); err != nil && !os.IsNotExist(err) {
				logger.Printf("remove sidecar %q: %v", descPath, err)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			logger.Printf("image watcher error: %v", err)
		case event, ok := <-w.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 && IsImage(event.Name) {
				source := ImagePathPrefix + filepath.Base(event.Name)
				if err := removeSource(ctx, source, documents, vectors); err != nil {
					logger.Printf("remove image source %q: %v", event.Name, err)
				}
				_ = os.Remove(event.Name + ".description")
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
						logger.Printf("watch image dir %q: %v", event.Name, err)
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
