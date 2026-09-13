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

type TextGenerator interface {
	Chat(ctx context.Context, messages []Message) (Message, error)
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Client struct {
	cfg         config.Config
	textModel   provider.LanguageModel
	visionModel provider.LanguageModel
}

func New(cfg config.Config) *Client {
	visionModel := provider.LanguageModel(nil)
	if cfg.VisionModel != "" {
		visionModel = cloudflare.Chat(
			cfg.VisionModel,
			cloudflare.WithAccountID(cfg.AccountID),
			cloudflare.WithAPIKey(cfg.APIToken),
		)
	}

	return &Client{
		cfg: cfg,
		textModel: cloudflare.Chat(
			cfg.Model,
			cloudflare.WithAccountID(cfg.AccountID),
			cloudflare.WithAPIKey(cfg.APIToken),
		),
		visionModel: visionModel,
	}
}

func (c *Client) ChatStream(ctx context.Context, messages []Message, onTextChunk func(string)) (Message, error) {
	providerMessages, err := toProviderMessages(messages)
	if err != nil {
		return Message{}, err
	}

	stream, err := goai.StreamText(ctx, c.textModel, goai.WithMessages(providerMessages...))
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

func (c *Client) Chat(ctx context.Context, messages []Message) (Message, error) {
	providerMessages, err := toProviderMessages(messages)
	if err != nil {
		return Message{}, err
	}

	result, err := goai.GenerateText(ctx, c.textModel, goai.WithMessages(providerMessages...))
	if err != nil {
		return Message{}, err
	}

	return Message{Role: "assistant", Content: result.Text}, nil
}

func toProviderMessages(messages []Message) ([]provider.Message, error) {
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
			return nil, fmt.Errorf("unsupported message role %q", message.Role)
		}
	}
	return providerMessages, nil
}
