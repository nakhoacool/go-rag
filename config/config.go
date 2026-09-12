package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	AccountID            string
	APIToken             string
	Model                string
	D1DatabaseID         string
	VectorizeIndex       string
	VectorizeDimensions  string
	JinaEmbeddingBaseURL string
	JinaAPIKey           string
	JinaEmbeddingModel   string
	IngestDir            string
	ProcessedDir         string
	SystemPromptFile     string

	HTTPAddr    string
	ImagesDir   string
	VisionModel string
}

func Load() Config {
	_ = godotenv.Load()

	cfg := Config{
		AccountID:            os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		APIToken:             os.Getenv("CLOUDFLARE_API_TOKEN"),
		Model:                os.Getenv("CLOUDFLARE_MODEL"),
		D1DatabaseID:         os.Getenv("CLOUDFLARE_D1_DATABASE_ID"),
		VectorizeIndex:       os.Getenv("CLOUDFLARE_VECTORIZE_INDEX"),
		VectorizeDimensions:  os.Getenv("CLOUDFLARE_VECTORIZE_DIMENSIONS"),
		JinaEmbeddingBaseURL: os.Getenv("JINA_EMBEDDING_BASE_URL"),
		JinaAPIKey:           os.Getenv("JINA_API_KEY"),
		JinaEmbeddingModel:   os.Getenv("JINA_EMBEDDING_MODEL"),
		IngestDir:            os.Getenv("INGEST_DIR"),
		ProcessedDir:         os.Getenv("PROCESSED_DIR"),
		SystemPromptFile:     os.Getenv("SYSTEM_PROMPT_FILE"),
		HTTPAddr:             os.Getenv("HTTP_ADDR"),
		ImagesDir:            os.Getenv("IMAGES_DIR"),
		VisionModel:          os.Getenv("VISION_MODEL"),
	}

	if cfg.IngestDir == "" {
		cfg.IngestDir = "./document/ingest"
	}

	if cfg.ProcessedDir == "" {
		cfg.ProcessedDir = "./document/processed"
	}

	if cfg.ImagesDir == "" {
		cfg.ImagesDir = "./document/images"
	}

	return cfg
}
