package cloudflare

import (
	"go-rag/config"

	"github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/d1"
	"github.com/cloudflare/cloudflare-go/v7/option"
	"github.com/cloudflare/cloudflare-go/v7/vectorize"
)

type Client struct {
	CF             *cloudflare.Client
	AccountID      string
	D1DatabaseID   string
	VectorizeIndex string
	D1             *d1.DatabaseService
	Vectorize      *vectorize.IndexService
}

func New(cfg config.Config) *Client {
	cf := cloudflare.NewClient(
		option.WithAPIToken(cfg.APIToken),
	)

	return &Client{
		CF:             cf,
		AccountID:      cfg.AccountID,
		D1DatabaseID:   cfg.D1DatabaseID,
		VectorizeIndex: cfg.VectorizeIndex,
		D1:             cf.D1.Database,
		Vectorize:      cf.Vectorize.Indexes,
	}
}
