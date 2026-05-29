package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gcal "google.golang.org/api/calendar/v3"
)

func main() {
	b, err := os.ReadFile("credentials.json")
	if err != nil {
		log.Fatalf("Baca credentials.json gagal: %v\nPastikan file ada di direktori yang sama.", err)
	}

	cfg, err := google.ConfigFromJSON(b, gcal.CalendarScope)
	if err != nil {
		log.Fatalf("Parse credentials: %v", err)
	}

	authURL := cfg.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Buka URL berikut di browser dan izinkan akses:\n\n%s\n\n", authURL)
	fmt.Print("Tempel authorization code di sini: ")

	var code string
	if _, err := fmt.Scan(&code); err != nil {
		log.Fatalf("Baca code: %v", err)
	}

	tok, err := cfg.Exchange(context.Background(), code)
	if err != nil {
		log.Fatalf("Exchange token: %v", err)
	}

	f, err := os.Create("token.json")
	if err != nil {
		log.Fatalf("Buat token.json: %v", err)
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(tok); err != nil {
		log.Fatalf("Tulis token.json: %v", err)
	}

	fmt.Println("\n✅ token.json berhasil dibuat! Sekarang jalankan: go run ./cmd/bot")
}
