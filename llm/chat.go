package llm

import (
	"context"
	"fmt"
	"go-rag/config"

	"github.com/zendev-sh/goai"
	"github.com/zendev-sh/goai/provider"
	"github.com/zendev-sh/goai/provider/cloudflare"
)

type Chat interface {
	ChatStream(ctx context.Context, messages []Message, onTextChunk func(string)) (Message, error)
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Client struct {
	cfg   config.Config
	model provider.LanguageModel
}

func New(cfg config.Config) *Client {
	return &Client{
		cfg: cfg,
		model: cloudflare.Chat(
			cfg.Model,
			cloudflare.WithAccountID(cfg.AccountID),
			cloudflare.WithAPIKey(cfg.APIToken),
		),
	}
}

func (c *Client) ChatStream(ctx context.Context, messages []Message, onTextChunk func(string)) (Message, error) {
	providerMessages := make([]provider.Message, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "system":
			providerMessages = append(providerMessages, goai.SystemMessage(message.Content))
		case "user":
			providerMessages = append(providerMessages, goai.UserMessage(message.Content))
		case "assistant":
			providerMessages = append(providerMessages, goai.AssistantMessage(message.Content))
		default:
			return Message{}, fmt.Errorf("unsupported message role %q", message.Role)
		}
	}

	stream, err := goai.StreamText(ctx, c.model, goai.WithMessages(providerMessages...))
	if err != nil {
		return Message{}, err
	}

	for delta := range stream.TextStream() {
		if onTextChunk != nil {
			onTextChunk(delta)
		}
	}

	if err := stream.Err(); err != nil {
		return Message{}, err
	}

	return Message{
		Role:    "assistant",
		Content: stream.Result().Text,
	}, nil
}
