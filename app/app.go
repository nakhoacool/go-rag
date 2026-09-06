package app

import (
	"context"
	"go-rag/chat"
	"go-rag/config"
	"go-rag/llm"
)

func Run(ctx context.Context, cfg config.Config) error {
	client := llm.New(cfg)
	return chat.RunREPL(ctx, client, chat.Options{
		SystemPromptFile: cfg.SystemPromptFile,
	})
}
