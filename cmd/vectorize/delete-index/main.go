package main

import (
	"context"
	"fmt"
	"log"

	"go-rag/cloudflare"
	"go-rag/config"

	cf "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/vectorize"
)

func main() {
	cfg := config.Load()
	if cfg.APIToken == "" || cfg.AccountID == "" || cfg.VectorizeIndex == "" {
		log.Fatal("CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID, and CLOUDFLARE_VECTORIZE_INDEX are required")
	}

	client := cloudflare.New(cfg)
	deletedIndex, err := client.Vectorize.Delete(context.Background(), cfg.VectorizeIndex, vectorize.IndexDeleteParams{
		AccountID: cf.F(cfg.AccountID),
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("deleted Vectorize index:\n %+v\n", deletedIndex)
}
