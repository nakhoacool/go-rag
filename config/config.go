package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	AccountID        string
	APIKey           string
	Model            string
	SystemPromptFile string
}

func Load() Config {
	_ = godotenv.Load()

	cfg := Config{
		AccountID:        os.Getenv("CLOUDFLARE_ACCOUNT_ID"),
		APIKey:           os.Getenv("CLOUDFLARE_API_TOKEN"),
		Model:            os.Getenv("CLOUDFLARE_MODEL"),
		SystemPromptFile: os.Getenv("SYSTEM_PROMPT_FILE"),
	}

	return cfg
}
