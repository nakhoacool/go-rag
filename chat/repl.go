package chat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"go-rag/llm"
	"go-rag/rag"
	"io/fs"
	"log"
	"os"
	"strings"
	"sync"
	"time"
)

type Options struct {
	SystemPromptFile string
	Retriever        rag.Retriever
	Rewriter         rag.Rewriter
}

func RunREPL(ctx context.Context, client llm.Chat, opts Options) error {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	history, err := seedHistory(opts.SystemPromptFile)
	if err != nil {
		return err
	}

	fmt.Println("Chat session started. Type Q to quit.")

	for {
		fmt.Print("\n> ")
		if !in.Scan() {
			if err := in.Err(); err != nil {
				return err
			}
			return nil
		}

		input := strings.TrimSpace(in.Text())
		if input == "" {
			continue
		}

		if strings.EqualFold(input, "q") || strings.EqualFold(input, "/exit") || strings.EqualFold(input, "exit") || strings.EqualFold(input, "quit") {
			fmt.Println("Goodbye.")
			return nil
		}

		turnStarted := time.Now()
		spin := startSpinner("thinking")
		var stopOnce sync.Once

		query := input
		var rewriteDuration time.Duration
		if opts.Rewriter != nil {
			rewriteStarted := time.Now()
			rewritten, err := opts.Rewriter.Rewrite(ctx, history, input)
			rewriteDuration = time.Since(rewriteStarted)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: rewrite query: %v; using original query\n", err)
			} else {
				query = rewritten
			}
		}

		history = append(history, llm.Message{Role: "user", Content: input})
		messages := history
		var retrieveDuration time.Duration
		if opts.Retriever != nil {
			retrieveStarted := time.Now()
			contextText, err := opts.Retriever.Retrieve(ctx, query)
			retrieveDuration = time.Since(retrieveStarted)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: retrieve context: %v\n", err)
				history = history[:len(history)-1]
				continue
			}
			if contextText != "" {
				messages = append([]llm.Message(nil), history[:len(history)-1]...)
				messages = append(messages, llm.Message{
					Role:    "user",
					Content: contextText + "\n\n--- Question ---\n\n" + input,
				})
			}
		}

		modelStarted := time.Now()
		var firstToken time.Time
		reply, err := client.ChatStream(ctx, messages, func(s string) {
			if firstToken.IsZero() {
				firstToken = time.Now()
			}
			stopOnce.Do(spin.Stop)
			fmt.Print(s)
		})
		modelDuration := time.Since(modelStarted)

		stopOnce.Do(spin.Stop)
		fmt.Println()
		if opts.Rewriter != nil {
			log.Printf("[repl] chat rewrite took %s", rewriteDuration)
		}
		if opts.Retriever != nil {
			log.Printf("[repl] chat retrieval took %s", retrieveDuration)
		}
		if firstToken.IsZero() {
			log.Printf("[repl] chat first token was not received")
		} else {
			log.Printf("[repl] chat first token after %s", firstToken.Sub(modelStarted))
		}
		log.Printf("[repl] chat model stream took %s; total turn took %s", modelDuration, time.Since(turnStarted))

		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			history = history[:len(history)-1]
			continue
		}
		history = append(history, reply)
	}
}

type spinner struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func startSpinner(label string) *spinner {
	s := &spinner{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go func() {
		defer close(s.done)
		frames := []string{"|", "/", "-", "\\"}
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Print("\r\033[K")
				return
			case <-t.C:
				fmt.Printf("\r%s %s", frames[i%len(frames)], label)
				i++
			}
		}
	}()
	return s
}

func (s *spinner) Stop() {
	s.once.Do(func() { close(s.stop) })
	<-s.done
}

func seedHistory(path string) ([]llm.Message, error) {
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read system prompt: %w", err)
	}

	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, nil
	}

	return []llm.Message{
		{Role: "system", Content: content},
	}, nil
}
