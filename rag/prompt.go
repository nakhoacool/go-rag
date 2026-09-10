package rag

import (
	"fmt"
	"go-rag/document"
	"go-rag/vector"
	"strings"
)

const contextPreamble = `Use the following excerpts from the document collection to answer the question. Cite sources by filename when you draw from them. If the excerpts do not address the question, say so before answer from general knowledge.`

const unknownSource = "(unknown source)"

func formatContext(hits []vector.Match, documents map[string]document.Document) string {
	if len(hits) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(contextPreamble)
	sb.WriteString("\n\n--- Excerpts ---\n\n")
	for i, h := range hits {
		source := h.Metadata["source_relative"]
		if source == "" {
			source = h.Metadata["source"]
		}
		if source == "" {
			source = unknownSource
		}
		content := ""
		if document, ok := documents[h.ID]; ok {
			content = document.Content
		}
		if content == "" {
			continue
		}
		fmt.Fprintf(&sb, "[%d] Source: %s (similarity %.2f)\n%s\n\n", i+1, source, h.Score, content)
	}

	return strings.TrimSpace(sb.String())
}
