package nlu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type ReminderItem struct {
	Title       string `json:"title"`
	Datetime    string `json:"datetime"`    // ISO 8601 with offset, or ""
	Recurring   string `json:"recurring"`   // daily | weekly | monthly | ""
	Description string `json:"description"` // konteks/deskripsi opsional
}

type EditItem struct {
	Query          string `json:"query"`
	NewTitle       string `json:"new_title"`
	NewDatetime    string `json:"new_datetime"`
	NewRecurring   string `json:"new_recurring"`
	NewDescription string `json:"new_description"`
}

type ParsedMessage struct {
	Action      string         `json:"action"`     // create | list | delete | edit | chat
	Items       []ReminderItem `json:"items"`
	EditItems   []EditItem     `json:"edit_items"`
	DeleteQuery string         `json:"delete_query"`
	DeleteIDs   []int64        `json:"delete_ids"`
	DeleteAll   bool           `json:"delete_all"`
	Reply       string         `json:"reply"`
}

type Parser struct {
	loc           *time.Location
	ollamaBaseURL string
	ollamaModel   string
}

func New(loc *time.Location, ollamaBaseURL, ollamaModel string) *Parser {
	return &Parser{loc: loc, ollamaBaseURL: ollamaBaseURL, ollamaModel: ollamaModel}
}

func (p *Parser) ParseMessage(ctx context.Context, userMsg string) (*ParsedMessage, error) {
	prompt := p.buildPrompt(userMsg)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Coba Claude CLI dulu
	raw, err := callClaude(ctx, prompt)
	if err != nil {
		// Fallback ke Ollama
		raw, err = p.callOllama(ctx, prompt)
		if err != nil {
			return nil, fmt.Errorf("claude dan ollama keduanya gagal: %w", err)
		}
	}

	raw = extractJSON(raw)

	var parsed ParsedMessage
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return &ParsedMessage{
			Action: "chat",
			Reply:  "Maaf, aku kurang mengerti. Bisa diulangi dengan lebih jelas? 😊",
		}, nil
	}
	return &parsed, nil
}

func (p *Parser) buildPrompt(userMsg string) string {
	now := time.Now().In(p.loc)
	nowStr := now.Format("Monday, 02 January 2006 15:04:05 MST")

	return fmt.Sprintf(`Kamu adalah asisten pribadi Telegram. Tugasmu: analisa pesan pengguna dan balas HANYA dengan satu JSON valid tanpa markdown fence.

Waktu sekarang: %s

Struktur JSON:
{
  "action": "create" | "list" | "delete" | "edit" | "query" | "chat",
  "items": [
    {"title": "string", "datetime": "RFC3339+07:00", "recurring": "daily|weekly|monthly|\"\"", "description": "konteks opsional"}
  ],
  "edit_items": [
    {"query": "judul/ID lama", "new_title": "", "new_datetime": "", "new_recurring": "", "new_description": ""}
  ],
  "delete_query": "", "delete_ids": [], "delete_all": false,
  "reply": "string Bahasa Indonesia ramah"
}

ATURAN PENTING:
1. action="create" → items berisi SEMUA jadwal. Sertakan description jika user menyebut konteks/keterangan.
2. datetime WAJIB format RFC3339 dengan offset +07:00, contoh: 2026-06-03T08:00:00+07:00
3. Waktu relatif: "besok"=%s, "lusa"=%s, "minggu depan"=Senin %s
4. Nama ruangan/lokasi → masuk ke title, konteks/keterangan → masuk ke description
5. action="edit" → edit_items berisi perubahan. Field new_* yang kosong = tidak diubah.
6. action="delete": "hapus semua"→delete_all=true, "hapus 1-5"→delete_ids, "hapus meeting"→delete_query
7. action="query" → PERTANYAAN tentang jadwal yang sudah ada. Contoh: "kapan ujian berikutnya?", "ada berapa jadwal minggu ini?", "besok ngapain aja?", "jadwal terdekat apa?". Hanya set action="query", field lain kosong.
8. action="list" → minta lihat SEMUA reminder ("tampilkan reminder", "/list").
9. reply SELALU diisi Bahasa Indonesia ramah

Pesan pengguna: %s`,
		nowStr,
		now.AddDate(0, 0, 1).Format("02 January 2006"),
		now.AddDate(0, 0, 2).Format("02 January 2006"),
		now.AddDate(0, 0, 7).Format("02 January 2006"),
		userMsg,
	)
}

// AnswerQuery answers a natural-language question using the user's real reminder data.
func (p *Parser) AnswerQuery(ctx context.Context, question, reminderContext string) (string, error) {
	now := time.Now().In(p.loc).Format("Monday, 02 January 2006 15:04 MST")
	prompt := fmt.Sprintf(`Kamu asisten pribadi. Jawab pertanyaan pengguna HANYA berdasar data jadwal di bawah. Jawab ringkas, ramah, Bahasa Indonesia, pakai emoji secukupnya. Jangan mengarang jadwal yang tidak ada.

Waktu sekarang: %s

DATA JADWAL PENGGUNA:
%s

PERTANYAAN: %s

Jawaban (teks biasa, bukan JSON):`, now, reminderContext, question)

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	ans, err := callClaude(ctx, prompt)
	if err != nil {
		ans, err = p.callOllama(ctx, prompt)
		if err != nil {
			return "", err
		}
	}
	return strings.TrimSpace(ans), nil
}

func callClaude(ctx context.Context, prompt string) (string, error) {
	cmd := exec.CommandContext(ctx, "claude", "-p", prompt, "--model", "claude-sonnet-4-6")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (p *Parser) callOllama(ctx context.Context, prompt string) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model":  p.ollamaModel,
		"stream": false,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.ollamaBaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama tidak tersedia: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || len(result.Choices) == 0 {
		return "", fmt.Errorf("ollama response invalid")
	}
	return result.Choices[0].Message.Content, nil
}

func extractJSON(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, "```"); idx != -1 {
		s = s[idx:]
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		if end := strings.LastIndex(s, "```"); end != -1 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start != -1 && end != -1 && end > start {
		return s[start : end+1]
	}
	return s
}
