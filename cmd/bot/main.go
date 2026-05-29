package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/ezranhmry/pf/internal/calendar"
	"github.com/ezranhmry/pf/internal/config"
	"github.com/ezranhmry/pf/internal/nlu"
	"github.com/ezranhmry/pf/internal/scheduler"
	"github.com/ezranhmry/pf/internal/store"
	"github.com/ezranhmry/pf/internal/transcribe"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		log.Fatalf("timezone: %v", err)
	}

	db, err := store.New("data.db")
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer db.Close()

	var cal *calendar.Client
	if cfg.UseGoogleCal {
		cal, err = calendar.Authorize("credentials.json", "token.json")
		if err != nil {
			log.Printf("calendar: disabled (%v)", err)
		}
	}

	parser := nlu.New(loc, cfg.OllamaBaseURL, cfg.OllamaModel)

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}
	log.Printf("Bot authorized: @%s", bot.Self.UserName)

	var ownerChatID int64
	fmt.Sscanf(cfg.TelegramChatID, "%d", &ownerChatID)

	sched := scheduler.New(db, bot, loc, ownerChatID)
	sched.Start()
	defer sched.Stop()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}
		go handleMessage(update.Message, bot, parser, db, cal, loc, cfg)
	}
}

func handleMessage(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	parser *nlu.Parser,
	db *store.Store,
	cal *calendar.Client,
	loc *time.Location,
	cfg *config.Config,
) {
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	ctx := context.Background()

	// Voice note
	if msg.Voice != nil {
		handleVoice(msg, bot, parser, db, cal, loc, cfg, chatID, ctx)
		return
	}

	// Pesan teks kosong
	if strings.TrimSpace(msg.Text) == "" {
		return
	}

	// /start command
	if msg.IsCommand() && msg.Command() == "start" {
		reply(bot, msg.Chat.ID, fmt.Sprintf(
			"Halo! Aku PF, asisten pribadimu 👋\nChat ID kamu: `%s`\n\nKirim pesan seperti:\n• \"Ingatkan aku meeting besok jam 9\"\n• \"Tampilkan reminder\"\n• \"Hapus reminder meeting\"",
			chatID,
		))
		return
	}

	parsed, err := parser.ParseMessage(ctx, msg.Text)
	if err != nil {
		log.Printf("nlu error: %v", err)
		reply(bot, msg.Chat.ID, "Maaf, ada gangguan sementara. Coba lagi ya 🙏")
		return
	}

	switch parsed.Action {
	case "create":
		handleCreate(msg, bot, db, cal, loc, chatID, parsed)
	case "list":
		handleList(msg, bot, db, loc, chatID, parsed)
	case "delete":
		handleDelete(msg, bot, db, cal, chatID, parsed)
	case "edit":
		handleEdit(msg, bot, db, cal, loc, chatID, parsed)
	default:
		reply(bot, msg.Chat.ID, parsed.Reply)
	}
}

func handleCreate(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	db *store.Store,
	cal *calendar.Client,
	loc *time.Location,
	chatID string,
	parsed *nlu.ParsedMessage,
) {
	items := parsed.Items
	if len(items) == 0 {
		reply(bot, msg.Chat.ID, "Kapan waktunya? Sebutkan tanggal dan jam ya 😊")
		return
	}

	var saved []string
	var failed []string

	for _, item := range items {
		if item.Datetime == "" {
			failed = append(failed, fmt.Sprintf("• %s (waktu tidak dikenali)", item.Title))
			continue
		}
		t, err := time.Parse(time.RFC3339, item.Datetime)
		if err != nil {
			failed = append(failed, fmt.Sprintf("• %s (format waktu salah)", item.Title))
			continue
		}

		calendarID := ""
		if cal != nil {
			id, err := cal.CreateEvent(item.Title, item.Description, t)
			if err != nil {
				log.Printf("calendar create event %q: %v", item.Title, err)
			} else {
				calendarID = id
			}
		}

		id, err := db.AddReminder(chatID, item.Title, item.Description, t.UTC().Format(time.RFC3339), item.Recurring, calendarID)
		if err != nil {
			log.Printf("add reminder %q: %v", item.Title, err)
			failed = append(failed, fmt.Sprintf("• %s (gagal simpan)", item.Title))
			continue
		}
		if id < 0 {
			// duplikat, skip
			continue
		}

		humanTime := t.In(loc).Format("Mon, 02 Jan 2006 15:04")
		entry := fmt.Sprintf("• %s — %s", item.Title, humanTime)
		if item.Recurring != "" {
			entry += fmt.Sprintf(" (%s)", item.Recurring)
		}
		saved = append(saved, entry)
	}

	var sb strings.Builder
	if len(saved) > 0 {
		sb.WriteString(fmt.Sprintf("✅ %d reminder disimpan:\n", len(saved)))
		for _, s := range saved {
			sb.WriteString(s + "\n")
		}
	}
	if len(failed) > 0 {
		sb.WriteString("\n⚠️ Gagal:\n")
		for _, f := range failed {
			sb.WriteString(f + "\n")
		}
	}
	if len(saved) == 0 && len(failed) == 0 {
		sb.WriteString("Semua reminder sudah ada sebelumnya, tidak ada yang ditambahkan.")
	}
	reply(bot, msg.Chat.ID, strings.TrimSpace(sb.String()))
}

func handleEdit(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	db *store.Store,
	cal *calendar.Client,
	loc *time.Location,
	chatID string,
	parsed *nlu.ParsedMessage,
) {
	if len(parsed.EditItems) == 0 {
		reply(bot, msg.Chat.ID, "Reminder mana yang mau diedit? Sebutkan judulnya ya.")
		return
	}

	var results []string
	for _, edit := range parsed.EditItems {
		matches, err := db.FindByQuery(chatID, edit.Query)
		if err != nil || len(matches) == 0 {
			results = append(results, fmt.Sprintf("• \"%s\" tidak ditemukan", edit.Query))
			continue
		}
		r := matches[0] // edit reminder pertama yang cocok

		// Terapkan perubahan, pertahankan nilai lama jika field kosong
		newTitle := r.Title
		if edit.NewTitle != "" {
			newTitle = edit.NewTitle
		}
		newDesc := r.Description
		if edit.NewDescription != "" {
			newDesc = edit.NewDescription
		}
		newRecurring := r.Recurring
		if edit.NewRecurring != "" {
			newRecurring = edit.NewRecurring
		}
		newTime := r.RemindAt
		if edit.NewDatetime != "" {
			if t, err := time.Parse(time.RFC3339, edit.NewDatetime); err == nil {
				newTime = t
			}
		}

		// Update Google Calendar
		if cal != nil && r.CalendarID != "" {
			if err := cal.UpdateEvent(r.CalendarID, newTitle, newDesc, newTime); err != nil {
				log.Printf("calendar update event %q: %v", r.CalendarID, err)
			}
		}

		if err := db.UpdateReminder(r.ID, newTitle, newDesc, newTime.UTC().Format(time.RFC3339), newRecurring, r.CalendarID); err != nil {
			log.Printf("update reminder %d: %v", r.ID, err)
			results = append(results, fmt.Sprintf("• \"%s\" gagal diperbarui", edit.Query))
			continue
		}

		humanTime := newTime.In(loc).Format("Mon, 02 Jan 2006 15:04")
		results = append(results, fmt.Sprintf("• %s — %s", newTitle, humanTime))
	}

	var sb strings.Builder
	if parsed.Reply != "" {
		sb.WriteString(parsed.Reply + "\n")
	}
	sb.WriteString("\n✏️ Hasil edit:\n")
	for _, r := range results {
		sb.WriteString(r + "\n")
	}
	reply(bot, msg.Chat.ID, strings.TrimSpace(sb.String()))
}

func handleList(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	db *store.Store,
	loc *time.Location,
	chatID string,
	parsed *nlu.ParsedMessage,
) {
	reminders, err := db.ListUpcoming(chatID)
	if err != nil {
		log.Printf("list reminders: %v", err)
		reply(bot, msg.Chat.ID, "Gagal mengambil daftar reminder.")
		return
	}

	if len(reminders) == 0 {
		reply(bot, msg.Chat.ID, "Tidak ada reminder yang aktif saat ini 😊")
		return
	}

	var sb strings.Builder
	sb.WriteString("📋 *Reminder aktif:*\n\n")
	for i, r := range reminders {
		humanTime := r.RemindAt.In(loc).Format("Mon, 02 Jan 2006 15:04")
		recurring := ""
		if r.Recurring != "" {
			recurring = fmt.Sprintf(" (%s)", r.Recurring)
		}
		sb.WriteString(fmt.Sprintf("%d. *%s*%s\n   🕐 %s\n", i+1, r.Title, recurring, humanTime))
		if r.Description != "" {
			sb.WriteString(fmt.Sprintf("   📝 %s\n", r.Description))
		}
	}

	out := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
	out.ParseMode = tgbotapi.ModeMarkdown
	if _, err := bot.Send(out); err != nil {
		log.Printf("send list: %v", err)
	}
}

func handleDelete(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	db *store.Store,
	cal *calendar.Client,
	chatID string,
	parsed *nlu.ParsedMessage,
) {
	// Ambil reminder yang akan dihapus untuk dapat calendar_id-nya
	var toDelete []store.Reminder
	var err error

	switch {
	case parsed.DeleteAll:
		toDelete, err = db.ListUpcoming(chatID)
	case len(parsed.DeleteIDs) > 0:
		toDelete, err = db.FindByIDs(chatID, parsed.DeleteIDs)
	case parsed.DeleteQuery != "":
		toDelete, err = db.FindByQuery(chatID, parsed.DeleteQuery)
	default:
		reply(bot, msg.Chat.ID, "Reminder mana yang ingin dihapus? Sebutkan judul, nomor, atau \"hapus semua\" ya.")
		return
	}

	if err != nil {
		log.Printf("delete find: %v", err)
		reply(bot, msg.Chat.ID, "Gagal mencari reminder.")
		return
	}
	if len(toDelete) == 0 {
		reply(bot, msg.Chat.ID, "Tidak ada reminder yang cocok untuk dihapus.")
		return
	}

	// Hapus dari Google Calendar
	if cal != nil {
		for _, r := range toDelete {
			if r.CalendarID == "" {
				continue
			}
			if err := cal.DeleteEvent(r.CalendarID); err != nil {
				log.Printf("calendar delete event %q: %v", r.CalendarID, err)
			}
		}
	}

	// Hapus dari DB
	var n int
	switch {
	case parsed.DeleteAll:
		n, err = db.DeleteAll(chatID)
	case len(parsed.DeleteIDs) > 0:
		n, err = db.DeleteByIDs(chatID, parsed.DeleteIDs)
	default:
		n, err = db.DeleteReminder(chatID, parsed.DeleteQuery)
	}

	if err != nil {
		log.Printf("delete reminder: %v", err)
		reply(bot, msg.Chat.ID, "Gagal menghapus reminder.")
		return
	}

	text := parsed.Reply
	if text == "" {
		text = fmt.Sprintf("🗑️ %d reminder berhasil dihapus!", n)
	}
	reply(bot, msg.Chat.ID, text)
}

func handleVoice(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	parser *nlu.Parser,
	db *store.Store,
	cal *calendar.Client,
	loc *time.Location,
	cfg *config.Config,
	chatID string,
	ctx context.Context,
) {
	reply(bot, msg.Chat.ID, "🎤 Memproses voice note...")

	fileURL, err := bot.GetFileDirectURL(msg.Voice.FileID)
	if err != nil {
		log.Printf("get voice file url: %v", err)
		reply(bot, msg.Chat.ID, "Gagal mengambil voice note. Coba lagi ya.")
		return
	}

	tmpFile, err := os.CreateTemp("", "voice-*.ogg")
	if err != nil {
		log.Printf("create temp file: %v", err)
		reply(bot, msg.Chat.ID, "Gagal memproses voice note.")
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := downloadFile(fileURL, tmpFile); err != nil {
		log.Printf("download voice: %v", err)
		reply(bot, msg.Chat.ID, "Gagal mengunduh voice note.")
		return
	}
	tmpFile.Close()

	text, err := transcribe.Transcribe(tmpPath, cfg.WhisperModel)
	if err != nil {
		log.Printf("transcribe: %v", err)
		reply(bot, msg.Chat.ID, "Gagal mentranskrip voice note. Pastikan Whisper sudah terinstall (`pip install openai-whisper`).")
		return
	}
	if text == "" {
		reply(bot, msg.Chat.ID, "Voice note tidak bisa ditranskrip. Coba kirim ulang dengan lebih jelas ya.")
		return
	}

	// Konfirmasi transkripsi ke user
	reply(bot, msg.Chat.ID, fmt.Sprintf("🎤 *Transkripsi:* _%s_", escapeMarkdownItalic(text)))

	// Proses seperti pesan teks biasa
	parsed, err := parser.ParseMessage(ctx, text)
	if err != nil {
		log.Printf("nlu error (voice): %v", err)
		reply(bot, msg.Chat.ID, "Maaf, ada gangguan sementara. Coba lagi ya 🙏")
		return
	}

	switch parsed.Action {
	case "create":
		handleCreate(msg, bot, db, cal, loc, chatID, parsed)
	case "list":
		handleList(msg, bot, db, loc, chatID, parsed)
	case "delete":
		handleDelete(msg, bot, db, cal, chatID, parsed)
	case "edit":
		handleEdit(msg, bot, db, cal, loc, chatID, parsed)
	default:
		reply(bot, msg.Chat.ID, parsed.Reply)
	}
}

func downloadFile(url string, dst *os.File) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(dst, resp.Body)
	return err
}

func escapeMarkdownItalic(s string) string {
	return strings.NewReplacer("_", "\\_", "*", "\\*", "`", "\\`", "[", "\\[").Replace(s)
}

func reply(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := bot.Send(msg); err != nil {
		log.Printf("send message: %v", err)
	}
}
