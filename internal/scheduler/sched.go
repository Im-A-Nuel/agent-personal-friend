package scheduler

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/robfig/cron/v3"

	"github.com/ezranhmry/pf/internal/store"
)

type Scheduler struct {
	c      *cron.Cron
	store  *store.Store
	bot    *tgbotapi.BotAPI
	loc    *time.Location
	chatID int64
}

func New(s *store.Store, bot *tgbotapi.BotAPI, loc *time.Location, chatID int64) *Scheduler {
	return &Scheduler{
		c:      cron.New(cron.WithLocation(loc)),
		store:  s,
		bot:    bot,
		loc:    loc,
		chatID: chatID,
	}
}

func (s *Scheduler) Start() {
	s.c.AddFunc("* * * * *", s.tick)
	s.c.AddFunc("0 7 * * *", s.sendMorningSummary)
	s.c.AddFunc("0 19 * * 0", s.sendWeeklyPreview) // Minggu 19:00 → preview minggu depan
	s.c.Start()
}

func (s *Scheduler) Stop() context.Context {
	return s.c.Stop()
}

func (s *Scheduler) tick() {
	now := time.Now().In(s.loc)

	// Notifikasi tepat waktu
	due, err := s.store.GetDueReminders(now)
	if err != nil {
		log.Printf("scheduler: get due: %v", err)
	}
	for _, r := range due {
		humanTime := r.RemindAt.In(s.loc).Format("15:04")
		text := fmt.Sprintf("⏰ *Reminder*: %s\n🕐 %s", escapeMarkdown(r.Title), humanTime)
		if r.Description != "" {
			text += fmt.Sprintf("\n📝 %s", escapeMarkdown(r.Description))
		}
		s.sendMarkdown(r.ChatID, text)

		if r.Recurring != "" {
			next := nextOccurrence(r.RemindAt, r.Recurring)
			if err := s.store.RescheduleRecurring(r.ID, next); err != nil {
				log.Printf("scheduler: reschedule %d: %v", r.ID, err)
			}
		} else {
			if err := s.store.MarkNotified(r.ID); err != nil {
				log.Printf("scheduler: mark notified %d: %v", r.ID, err)
			}
		}
	}

	// Peringatan H-30 menit
	early, err := s.store.GetEarlyReminders(now, 30)
	if err != nil {
		log.Printf("scheduler: get early: %v", err)
	}
	for _, r := range early {
		humanTime := r.RemindAt.In(s.loc).Format("15:04")
		text := fmt.Sprintf("🔔 *30 menit lagi*: %s\n🕐 Jam %s", escapeMarkdown(r.Title), humanTime)
		if r.Description != "" {
			text += fmt.Sprintf("\n📝 %s", escapeMarkdown(r.Description))
		}
		s.sendMarkdown(r.ChatID, text)

		if err := s.store.MarkEarlyNotified(r.ID); err != nil {
			log.Printf("scheduler: mark early notified %d: %v", r.ID, err)
		}
	}
}

func (s *Scheduler) sendMorningSummary() {
	now := time.Now().In(s.loc)
	chatIDStr := fmt.Sprintf("%d", s.chatID)

	reminders, err := s.store.GetTodayReminders(chatIDStr, now)
	if err != nil {
		log.Printf("scheduler: morning summary: %v", err)
		return
	}

	if len(reminders) == 0 {
		s.send(s.chatID, "🌅 Selamat pagi! Tidak ada jadwal untuk hari ini. Nikmati harimu! 😊")
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🌅 *Selamat pagi!* Hari ini, %s\n\n", now.Format("Monday, 02 January 2006")))
	sb.WriteString("📋 *Jadwal kamu hari ini:*\n\n")
	for _, r := range reminders {
		jam := r.RemindAt.In(s.loc).Format("15:04")
		sb.WriteString(fmt.Sprintf("🕐 *%s* — %s\n", jam, escapeMarkdown(r.Title)))
	}
	sb.WriteString("\nSemangat! 💪")

	msg := tgbotapi.NewMessage(s.chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := s.bot.Send(msg); err != nil {
		log.Printf("scheduler: send morning summary: %v", err)
	}
}

func (s *Scheduler) sendWeeklyPreview() {
	now := time.Now().In(s.loc)
	chatIDStr := fmt.Sprintf("%d", s.chatID)

	start := now
	end := now.AddDate(0, 0, 7)
	reminders, err := s.store.GetRange(chatIDStr, start, end)
	if err != nil {
		log.Printf("scheduler: weekly preview: %v", err)
		return
	}

	if len(reminders) == 0 {
		s.send(s.chatID, "📆 Minggu depan belum ada jadwal tersimpan. Santai dulu! 😎")
		return
	}

	var sb strings.Builder
	sb.WriteString("📆 *Preview Jadwal Minggu Depan:*\n\n")
	lastDay := ""
	for _, r := range reminders {
		day := r.RemindAt.In(s.loc).Format("Monday, 02 January")
		if day != lastDay {
			sb.WriteString(fmt.Sprintf("\n📅 *%s*\n", day))
			lastDay = day
		}
		jam := r.RemindAt.In(s.loc).Format("15:04")
		sb.WriteString(fmt.Sprintf("  🕐 %s — %s\n", jam, escapeMarkdown(r.Title)))
	}
	sb.WriteString("\nSiapkan dirimu! 💪")

	msg := tgbotapi.NewMessage(s.chatID, sb.String())
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := s.bot.Send(msg); err != nil {
		log.Printf("scheduler: send weekly preview: %v", err)
	}
}

func (s *Scheduler) sendMarkdown(chatIDStr, text string) {
	var id int64
	fmt.Sscanf(chatIDStr, "%d", &id)
	msg := tgbotapi.NewMessage(id, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := s.bot.Send(msg); err != nil {
		log.Printf("scheduler: send to %s: %v", chatIDStr, err)
	}
}

func (s *Scheduler) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := s.bot.Send(msg); err != nil {
		log.Printf("scheduler: send: %v", err)
	}
}

func nextOccurrence(t time.Time, recurring string) time.Time {
	switch recurring {
	case "daily":
		return t.AddDate(0, 0, 1)
	case "weekly":
		return t.AddDate(0, 0, 7)
	case "monthly":
		return t.AddDate(0, 1, 0)
	default:
		return t.AddDate(0, 0, 1)
	}
}

func escapeMarkdown(s string) string {
	r := strings.NewReplacer("_", "\\_", "*", "\\*", "[", "\\[", "`", "\\`")
	return r.Replace(s)
}
