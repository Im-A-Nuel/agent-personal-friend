package config

import (
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	TelegramBotToken string
	TelegramChatID   string
	Timezone         string
	UseGoogleCal     bool
	WhisperModel     string
	OllamaBaseURL    string
	OllamaModel      string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	tz := os.Getenv("TIMEZONE")
	if tz == "" {
		tz = "Asia/Jakarta"
	}

	useGCal := os.Getenv("USE_GOOGLE_CALENDAR") == "true"

	whisperModel := os.Getenv("WHISPER_MODEL")
	if whisperModel == "" {
		whisperModel = "small"
	}

	ollamaURL := os.Getenv("OLLAMA_BASE_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	ollamaModel := os.Getenv("OLLAMA_MODEL")
	if ollamaModel == "" {
		ollamaModel = "llama3.2"
	}

	return &Config{
		TelegramBotToken: os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID:   os.Getenv("TELEGRAM_CHAT_ID"),
		Timezone:         tz,
		UseGoogleCal:     useGCal,
		WhisperModel:     whisperModel,
		OllamaBaseURL:    ollamaURL,
		OllamaModel:      ollamaModel,
	}, nil
}
