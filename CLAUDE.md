# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project
PF (Personal Friend) — Telegram bot (Go) yang jadi asisten pribadi: parsing bahasa natural (Bahasa Indonesia), bikin reminder, auto-create Google Calendar event, dan kirim notifikasi terjadwal.

## Stack
- Language: Go 1.22+
- Bot: `github.com/go-telegram-bot-api/telegram-bot-api/v5` (long polling)
- AI/NLU: Claude CLI (`claude -p "..."`) via `os/exec` — tidak butuh API key
- Database: `database/sql` + `modernc.org/sqlite` (pure Go, no CGO)
- Calendar: `google.golang.org/api/calendar/v3` + `golang.org/x/oauth2/google`
- Scheduler: `github.com/robfig/cron/v3`
- Config: `github.com/joho/godotenv`

## Commands
```bash
# Download dependencies
go mod tidy

# One-time Google Calendar OAuth (generates token.json)
go run ./cmd/auth

# Run bot
go run ./cmd/bot

# Static binary (no CGO) — Linux/Mac
CGO_ENABLED=0 go build -o asisten-bot ./cmd/bot

# Static binary — Windows PowerShell
$env:CGO_ENABLED=0; go build -o asisten-bot.exe ./cmd/bot
```

## Project Structure
```
cmd/
  bot/main.go        # entry point: init bot, handler, start scheduler
  auth/main.go       # Google Calendar OAuth helper
internal/
  config/config.go   # load .env → Config struct
  nlu/nlu.go         # ParseMessage(): natural text → ParsedMessage struct
  store/store.go     # SQLite: AddReminder, GetDueReminders, MarkNotified, RescheduleRecurring, ListUpcoming, DeleteReminder
  calendar/cal.go    # Authorize(), CreateEvent()
  scheduler/sched.go # cron every minute: check due → notify → reschedule or MarkNotified
```

## Data Flow
```
User message → cmd/bot handler → nlu.ParseMessage() → struct action:
  "create"  → store.AddReminder() + calendar.CreateEvent() (optional)
  "list"    → store.ListUpcoming()
  "delete"  → store.DeleteReminder()
  "chat"    → direct reply
Scheduler (every minute) → GetDueReminders() → Telegram notify → RescheduleRecurring() or MarkNotified()
```

## NLU Contract
`ParseMessage()` sends user message + current time to Claude, expects JSON unmarshalled into:
```go
type ParsedMessage struct {
    Action      string `json:"action"`       // "create" | "list" | "delete" | "chat"
    Title       string `json:"title"`
    Datetime    string `json:"datetime"`      // ISO 8601 + offset: "2026-05-30T14:00:00+07:00" or ""
    Recurring   string `json:"recurring"`     // "daily" | "weekly" | "monthly" | ""
    DeleteQuery string `json:"delete_query"`
    Reply       string `json:"reply"`         // Bahasa Indonesia, friendly
}
```
- Current time is injected into the system prompt via `time.Now().In(loc)` — never hardcode dates.
- Strip ` ```json ` fences before unmarshal.
- On unmarshal failure: fallback to `Action: "chat"` with a clarification reply.

## Key Conventions
- User-facing replies: **Bahasa Indonesia**, friendly, light emoji.
- Timezone: `time.LoadLocation(cfg.Timezone)`, default `Asia/Jakarta`.
- Datetimes stored as RFC3339 strings in DB; displayed in human format (e.g. "Jumat, 30 Mei 2026 14:00").
- Chat ID stored as TEXT (int64 formatted to string).
- Anthropic calls: always use `context.WithTimeout` (30s).

## Architecture Constraints
- **SQLite driver**: use only `modernc.org/sqlite` (pure Go). Do NOT switch to `mattn/go-sqlite3` (requires CGO/gcc).
- **Concurrent DB access**: open connection with `?_busy_timeout=5000` or serialize via a single connection.
- **Google Calendar is optional** (`USE_GOOGLE_CALENDAR=true|false`). Calendar errors must NOT fail `AddReminder()` — wrap calendar call separately, log the error, continue.
- **Recurring reminders**: after notifying, `RescheduleRecurring()` advances `remind_at` by +1 day/week/month via `t.AddDate` and resets `notified=0`. Non-recurring: `MarkNotified()`.
- **Long polling** (not webhook) — requires a persistent process (systemd/VPS), not serverless.
- Do not install new dependencies without confirming with the user.
- Do not change the model string `claude-sonnet-4-6` without confirming.

## DB Schema
```sql
CREATE TABLE IF NOT EXISTS reminders (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_id     TEXT NOT NULL,
  title       TEXT NOT NULL,
  remind_at   TEXT NOT NULL,         -- RFC3339
  recurring   TEXT DEFAULT NULL,     -- daily | weekly | monthly | NULL
  calendar_id TEXT DEFAULT NULL,
  notified    INTEGER DEFAULT 0,
  created_at  TEXT DEFAULT (datetime('now'))
);
```

## Environment Variables (.env)
| Variable | Description |
|---|---|
| `TELEGRAM_BOT_TOKEN` | From @BotFather |
| `TELEGRAM_CHAT_ID` | User's chat ID (shown on /start) |
| `TIMEZONE` | Default: `Asia/Jakarta` |
| `USE_GOOGLE_CALENDAR` | `true` or `false` |

Files never to commit: `.env`, `credentials.json`, `token.json`, `data.db`.

## Current Focus
MVP build order: `config` → `store` → `nlu` → `calendar` → `scheduler` → `cmd/bot`.
MVP features: reminder via chat, Google Calendar sync, recurring (daily/weekly/monthly), list & delete.
Post-MVP: voice note transcription, edit existing reminders.
