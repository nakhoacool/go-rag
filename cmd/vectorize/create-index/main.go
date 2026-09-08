package main

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"go-rag/cloudflare"
	"go-rag/config"

	cf "github.com/cloudflare/cloudflare-go/v7"
	"github.com/cloudflare/cloudflare-go/v7/vectorize"
)

func main() {
	cfg := config.Load()
	if cfg.APIToken == "" || cfg.AccountID == "" || cfg.VectorizeIndex == "" || cfg.VectorizeDimensions == "" {
		log.Fatal("CLOUDFLARE_API_TOKEN, CLOUDFLARE_ACCOUNT_ID, CLOUDFLARE_VECTORIZE_INDEX and CLOUDFLARE_VECTORIZE_DIMENSIONS are required")
	}

	dimensions, err := strconv.ParseInt(cfg.VectorizeDimensions, 10, 64)
	if err != nil || dimensions <= 0 {
		log.Fatalf("invalid CLOUDFLARE_VECTORIZE_DIMENSIONS: %q", cfg.VectorizeDimensions)
	}

	client := cloudflare.New(cfg)
	createIndex, err := client.Vectorize.New(context.Background(), vectorize.IndexNewParams{
		AccountID: cf.F(cfg.AccountID),
		Config: cf.F[vectorize.IndexNewParamsConfigUnion](vectorize.IndexDimensionConfigurationParam{
			Dimensions: cf.F(dimensions),
			Metric:     cf.F(vectorize.IndexDimensionConfigurationMetricCosine),
		}),
		Name: cf.F(cfg.VectorizeIndex),
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("created Vectorize index:\n %+v\n", createIndex.Config)
}
