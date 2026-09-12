package rag

import (
	"context"
	"fmt"
	"go-rag/llm"
	"strings"
)

const rewriterSystemPrompt = `Rewrite the user's latest question into a standalone search query for retrieving relevant documents.

Use the conversation history to resolve pronouns and references. Preserve the user's intent and important names, terms, and constraints. 

If the latest user message already stands on its own with no references to prior turns, output it verbatim.

Return only the search query, with no explanation, quotes, or additional formatting.
`

type Rewriter interface {
	Rewrite(ctx context.Context, history []llm.Message, question string) (string, error)
}

type rewriter struct {
	client llm.TextGenerator
}

func NewRewriter(client llm.TextGenerator) Rewriter {
	return &rewriter{client: client}
}

func (r *rewriter) Rewrite(ctx context.Context, history []llm.Message, question string) (string, error) {
	messages := make([]llm.Message, 0, len(history)+2)
	messages = append(messages, llm.Message{Role: "system", Content: rewriterSystemPrompt})
	for _, message := range history {
		if message.Role != "system" {
			messages = append(messages, message)
		}
	}
	messages = append(messages, llm.Message{Role: "user", Content: question})

	reply, err := r.client.Chat(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("rewrite query: %w", err)
	}
	query := strings.TrimSpace(reply.Content)
	query = strings.Trim(query, `"'`)
	if query == "" {
		return "", fmt.Errorf("rewrite query: empty response")
	}
	return query, nil
}
