package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/ezranhmry/pf/internal/nlu"
	"github.com/ezranhmry/pf/internal/store"
)

// ---------- TASKS ----------

func handleTaskCreate(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	db *store.Store,
	loc *time.Location,
	chatID string,
	parsed *nlu.ParsedMessage,
) {
	if len(parsed.Tasks) == 0 {
		reply(bot, msg.Chat.ID, "Tugasnya apa? Sebutkan ya 😊")
		return
	}

	var saved []string
	for _, t := range parsed.Tasks {
		due := ""
		if t.Due != "" {
			if parsedTime, err := time.Parse(time.RFC3339, t.Due); err == nil {
				due = parsedTime.UTC().Format(time.RFC3339)
			}
		}
		if _, err := db.AddTask(chatID, t.Title, t.Priority, due); err != nil {
			log.Printf("add task %q: %v", t.Title, err)
			continue
		}
		entry := fmt.Sprintf("%s %s", priorityEmoji(t.Priority), t.Title)
		if due != "" {
			dt, _ := time.Parse(time.RFC3339, due)
			entry += fmt.Sprintf(" (deadline %s)", dt.In(loc).Format("Mon, 02 Jan 15:04"))
		}
		saved = append(saved, entry)
	}

	if len(saved) == 0 {
		reply(bot, msg.Chat.ID, "Gagal menyimpan tugas. Coba lagi ya.")
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("✅ %d tugas ditambahkan:\n", len(saved)))
	for _, s := range saved {
		sb.WriteString("• " + s + "\n")
	}
	reply(bot, msg.Chat.ID, strings.TrimSpace(sb.String()))
}

func handleTaskList(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	db *store.Store,
	loc *time.Location,
	chatID string,
) {
	tasks, err := db.ListTasks(chatID, false)
	if err != nil {
		log.Printf("list tasks: %v", err)
		reply(bot, msg.Chat.ID, "Gagal membaca daftar tugas.")
		return
	}
	if len(tasks) == 0 {
		reply(bot, msg.Chat.ID, "Tidak ada tugas yang belum selesai. Mantap! 🎉")
		return
	}

	var sb strings.Builder
	sb.WriteString("📝 *Daftar Tugas:*\n\n")
	for i, t := range tasks {
		line := fmt.Sprintf("%d. %s %s", i+1, priorityEmoji(t.Priority), t.Title)
		if t.Due != nil {
			line += fmt.Sprintf("\n   ⏳ deadline: %s", t.Due.In(loc).Format("Mon, 02 Jan 2006 15:04"))
		}
		sb.WriteString(line + "\n")
	}

	out := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
	out.ParseMode = tgbotapi.ModeMarkdown
	if _, err := bot.Send(out); err != nil {
		log.Printf("send task list: %v", err)
	}
}

func handleTaskDone(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	db *store.Store,
	chatID string,
	parsed *nlu.ParsedMessage,
) {
	if parsed.TaskQuery == "" {
		reply(bot, msg.Chat.ID, "Tugas mana yang selesai? Sebutkan judul atau nomornya.")
		return
	}
	n, err := db.MarkTaskDone(chatID, parsed.TaskQuery)
	if err != nil {
		log.Printf("mark task done: %v", err)
		reply(bot, msg.Chat.ID, "Gagal menandai tugas.")
		return
	}
	if n == 0 {
		reply(bot, msg.Chat.ID, "Tugas tidak ditemukan.")
		return
	}
	text := parsed.Reply
	if text == "" {
		text = fmt.Sprintf("🎉 %d tugas selesai! Kerja bagus!", n)
	}
	reply(bot, msg.Chat.ID, text)
}

func handleTaskDelete(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	db *store.Store,
	chatID string,
	parsed *nlu.ParsedMessage,
) {
	if parsed.TaskQuery == "" {
		reply(bot, msg.Chat.ID, "Tugas mana yang dihapus? Sebutkan judul atau nomornya.")
		return
	}
	n, err := db.DeleteTask(chatID, parsed.TaskQuery)
	if err != nil {
		log.Printf("delete task: %v", err)
		reply(bot, msg.Chat.ID, "Gagal menghapus tugas.")
		return
	}
	if n == 0 {
		reply(bot, msg.Chat.ID, "Tugas tidak ditemukan.")
		return
	}
	reply(bot, msg.Chat.ID, fmt.Sprintf("🗑️ %d tugas dihapus.", n))
}

// ---------- NOTES ----------

func handleNoteCreate(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	db *store.Store,
	chatID string,
	parsed *nlu.ParsedMessage,
) {
	if len(parsed.Notes) == 0 {
		reply(bot, msg.Chat.ID, "Mau catat apa? 😊")
		return
	}
	count := 0
	for _, n := range parsed.Notes {
		if strings.TrimSpace(n.Content) == "" {
			continue
		}
		if _, err := db.AddNote(chatID, n.Content, n.Tags); err != nil {
			log.Printf("add note: %v", err)
			continue
		}
		count++
	}
	if count == 0 {
		reply(bot, msg.Chat.ID, "Gagal menyimpan catatan.")
		return
	}
	text := parsed.Reply
	if text == "" {
		text = fmt.Sprintf("📌 %d catatan tersimpan!", count)
	}
	reply(bot, msg.Chat.ID, text)
}

func handleNoteList(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	db *store.Store,
	loc *time.Location,
	chatID string,
) {
	notes, err := db.ListNotes(chatID)
	if err != nil {
		log.Printf("list notes: %v", err)
		reply(bot, msg.Chat.ID, "Gagal membaca catatan.")
		return
	}
	renderNotes(msg, bot, notes, loc, "📒 *Catatan kamu:*")
}

func handleNoteSearch(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	db *store.Store,
	loc *time.Location,
	chatID string,
	parsed *nlu.ParsedMessage,
) {
	if parsed.NoteQuery == "" {
		reply(bot, msg.Chat.ID, "Mau cari catatan apa? Sebutkan kata kuncinya.")
		return
	}
	notes, err := db.SearchNotes(chatID, parsed.NoteQuery)
	if err != nil {
		log.Printf("search notes: %v", err)
		reply(bot, msg.Chat.ID, "Gagal mencari catatan.")
		return
	}
	renderNotes(msg, bot, notes, loc, fmt.Sprintf("🔍 *Hasil pencarian \"%s\":*", parsed.NoteQuery))
}

func handleNoteDelete(
	msg *tgbotapi.Message,
	bot *tgbotapi.BotAPI,
	db *store.Store,
	chatID string,
	parsed *nlu.ParsedMessage,
) {
	if parsed.NoteQuery == "" {
		reply(bot, msg.Chat.ID, "Catatan mana yang dihapus? Sebutkan kata kunci atau nomornya.")
		return
	}
	n, err := db.DeleteNote(chatID, parsed.NoteQuery)
	if err != nil {
		log.Printf("delete note: %v", err)
		reply(bot, msg.Chat.ID, "Gagal menghapus catatan.")
		return
	}
	if n == 0 {
		reply(bot, msg.Chat.ID, "Catatan tidak ditemukan.")
		return
	}
	reply(bot, msg.Chat.ID, fmt.Sprintf("🗑️ %d catatan dihapus.", n))
}

// ---------- HELPERS ----------

func renderNotes(msg *tgbotapi.Message, bot *tgbotapi.BotAPI, notes []store.Note, loc *time.Location, header string) {
	if len(notes) == 0 {
		reply(bot, msg.Chat.ID, "Tidak ada catatan yang cocok 😊")
		return
	}
	var sb strings.Builder
	sb.WriteString(header + "\n\n")
	for i, n := range notes {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, n.Content))
		if n.Tags != "" {
			sb.WriteString(fmt.Sprintf("   🏷️ %s\n", n.Tags))
		}
		if !n.CreatedAt.IsZero() {
			sb.WriteString(fmt.Sprintf("   🕐 %s\n", n.CreatedAt.In(loc).Format("02 Jan 2006 15:04")))
		}
	}
	out := tgbotapi.NewMessage(msg.Chat.ID, sb.String())
	out.ParseMode = tgbotapi.ModeMarkdown
	if _, err := bot.Send(out); err != nil {
		log.Printf("send notes: %v", err)
	}
}

func priorityEmoji(p string) string {
	switch p {
	case "high":
		return "🔴"
	case "low":
		return "🟢"
	default:
		return "🟡"
	}
}
