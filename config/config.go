package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	AccountID           string
	APIToken            string
	Model               string
	D1DatabaseID        string
	VectorizeIndex      string
	VectorizeDimensions string
	SystemPromptFile    string
}

func Load() Config {
	_ = godotenv.Load()

	cfg := Config{
		AccountID:           os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		APIToken:            os.Getenv("CLOUDFLARE_API_TOKEN"),
		Model:               os.Getenv("CLOUDFLARE_MODEL"),
		D1DatabaseID:        os.Getenv("CLOUDFLARE_D1_DATABASE_ID"),
		VectorizeIndex:      os.Getenv("CLOUDFLARE_VECTORIZE_INDEX"),
		VectorizeDimensions: os.Getenv("CLOUDFLARE_VECTORIZE_DIMENSIONS"),
		SystemPromptFile:    os.Getenv("SYSTEM_PROMPT_FILE"),
	}

	return cfg
}
