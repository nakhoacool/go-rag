package llm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/zendev-sh/goai"
	"github.com/zendev-sh/goai/provider"
)

const captionPrompt = "Describe this image in 2-3 sentences for a search index. Focus on the visible subject - what it is, key details, style. Do not interpret or speculate; describe only what is shown"

type Vision interface {
	HasVision() bool
	DescribeImage(context.Context, string, []byte) (string, error)
}

func (c *Client) HasVision() bool {
	return c.visionModel != nil
}

func (c *Client) DescribeImage(ctx context.Context, mime string, image []byte) (string, error) {
	if c.visionModel == nil {
		return "", errors.New("no vision model configured")
	}

	if len(image) == 0 {
		return "", errors.New("empty image")
	}

	if mime == "" {
		mime = http.DetectContentType(image)
	}

	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(image)
	result, err := goai.GenerateText(ctx, c.visionModel, goai.WithMessages(provider.Message{
		Role: provider.RoleUser,
		Content: []provider.Part{
			{Type: provider.PartText, Text: captionPrompt},
			{Type: provider.PartImage, URL: dataURL},
		},
	}))
	if err != nil {
		return "", fmt.Errorf("describe image: %w", err)
	}

	return strings.TrimSpace(result.Text), nil
}
