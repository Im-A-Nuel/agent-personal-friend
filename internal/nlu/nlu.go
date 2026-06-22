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

type TaskItem struct {
	Title    string `json:"title"`
	Priority string `json:"priority"` // low | normal | high
	Due      string `json:"due"`      // RFC3339+07:00 opsional, atau ""
}

type NoteItem struct {
	Content string `json:"content"`
	Tags    string `json:"tags"` // koma-separated, opsional
}

type ParsedMessage struct {
	Action      string         `json:"action"`
	Items       []ReminderItem `json:"items"`
	EditItems   []EditItem     `json:"edit_items"`
	DeleteQuery string         `json:"delete_query"`
	DeleteIDs   []int64        `json:"delete_ids"`
	DeleteAll   bool           `json:"delete_all"`
	Tasks       []TaskItem     `json:"tasks"`
	Notes       []NoteItem     `json:"notes"`
	TaskQuery   string         `json:"task_query"` // target done/delete task
	NoteQuery   string         `json:"note_query"` // target search/delete note

	// GitHub
	RepoName    string `json:"repo_name"`    // repo_create
	RepoDesc    string `json:"repo_desc"`    // repo_create
	RepoPrivate bool   `json:"repo_private"` // repo_create
	RepoOrg     string `json:"repo_org"`     // repo_create, "" = pribadi
	RepoTarget  string `json:"repo_target"`  // repo_work/repo_resolve: "owner/repo" atau "repo"
	RepoTask    string `json:"repo_task"`    // repo_work: instruksi yang dikerjakan
	RepoPR      int    `json:"repo_pr"`      // repo_resolve: nomor pull request

	Reply string `json:"reply"`
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
  "action": "create|list|delete|edit|query|chat|task_create|task_list|task_done|task_delete|note_create|note_list|note_search|note_delete|repo_create|repo_work",
  "items": [{"title": "", "datetime": "RFC3339+07:00", "recurring": "daily|weekly|monthly|\"\"", "description": ""}],
  "edit_items": [{"query": "", "new_title": "", "new_datetime": "", "new_recurring": "", "new_description": ""}],
  "delete_query": "", "delete_ids": [], "delete_all": false,
  "tasks": [{"title": "", "priority": "low|normal|high", "due": "RFC3339+07:00 atau kosong"}],
  "notes": [{"content": "", "tags": ""}],
  "task_query": "", "note_query": "",
  "repo_name": "", "repo_desc": "", "repo_private": false, "repo_org": "",
  "repo_target": "", "repo_task": "", "repo_pr": 0,
  "reply": "string Bahasa Indonesia ramah"
}

ATURAN — REMINDER (jadwal berwaktu, ada notifikasi):
1. action="create" → items berisi SEMUA jadwal. datetime WAJIB RFC3339 offset +07:00, mis: 2026-06-03T08:00:00+07:00
2. Waktu relatif: "besok"=%s, "lusa"=%s, "minggu depan"=Senin %s
3. Lokasi/ruangan→title, keterangan→description
4. action="edit" → edit_items. Field new_* kosong = tidak diubah.
5. action="delete": "hapus semua"→delete_all=true, "hapus 1-5"→delete_ids, "hapus meeting"→delete_query
6. action="query" → PERTANYAAN tentang jadwal. "kapan ujian?", "minggu ini ngapain?"
7. action="list" → lihat semua reminder.

ATURAN — TUGAS/TODO (hal yang harus dikerjakan, TANPA jam pasti):
8. action="task_create" → tasks[]. "tugasku: kerjakan laporan", "todo beli buku, prioritas tinggi". due opsional.
9. action="task_list" → "tugasku apa aja?", "lihat todo"
10. action="task_done" → task_query. "tugas laporan selesai", "selesaikan todo 2"
11. action="task_delete" → task_query. "hapus tugas laporan"

ATURAN — CATATAN/MEMO (informasi untuk diingat):
12. action="note_create" → notes[]. "catat: ide bikin app X", "memo nomor wifi 12345". tags opsional.
13. action="note_list" → "catatanku apa aja?", "lihat memo"
14. action="note_search" → note_query. "cari catatan wifi", "memo tentang ide"
15. action="note_delete" → note_query. "hapus catatan wifi"

BEDAKAN: reminder=ada jam+notifikasi. task=harus dikerjakan tanpa jam pasti. note=info disimpan.

ATURAN — GITHUB:
16. action="repo_create" → buat repo baru. "buat repo namanya X", "bikin repository private Y", "buat repo Z di organisasi ABC".
    - repo_name=nama repo, repo_desc=deskripsi (opsional), repo_org=nama org jika disebut (kosong=repo pribadi)
    - repo_private: WAJIB true jika ada kata "private"/"privat"/"rahasia"/"tertutup". false jika "public"/"publik" atau tidak disebut.
    - Contoh: "buat repo test, private" → {repo_name:"test", repo_private:true}
    - Contoh: "buat repo blog" → {repo_name:"blog", repo_private:false}
17. action="repo_work" → kerjakan coding di repo. "di repo X kerjakan: tambah fitur login", "perbaiki bug di repo owner/Y", "tambahkan endpoint di repo Z".
    - repo_target=nama repo (boleh "owner/repo" atau "repo" saja), repo_task=instruksi lengkap yang harus dikerjakan
18. action="repo_resolve" → selesaikan merge conflict pada pull request. "fix conflict PR 3 di repo X", "selesaikan conflict pull request #5 repo Y", "resolve konflik PR 2 repo Z".
    - repo_target=nama repo, repo_pr=nomor PR (angka saja, tanpa #)
19. reply SELALU diisi Bahasa Indonesia ramah.

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
