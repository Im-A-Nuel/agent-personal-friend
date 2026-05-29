# PF — Personal Friend

Telegram bot asisten pribadi berbasis AI. Parsing pesan bahasa natural (Bahasa Indonesia), membuat reminder, sinkronisasi Google Calendar, dan mengirim notifikasi terjadwal.

## Fitur

- **Reminder via chat** — buat reminder dengan kalimat natural, satu atau banyak sekaligus
- **Batch input** — "ingatkan ujian Rabu jam 8, Kamis jam 10, Jumat jam 13" → semua tersimpan sekaligus
- **Edit reminder** — ubah judul, waktu, atau deskripsi reminder yang sudah ada
- **Deskripsi/konteks** — tambahkan catatan ke setiap reminder
- **Recurring** — pengulangan harian, mingguan, atau bulanan
- **Google Calendar sync** — event otomatis dibuat/dihapus/diedit di Google Calendar
- **Morning summary** — setiap pagi jam 07:00, bot kirim daftar jadwal hari itu
- **Notifikasi H-30 menit** — peringatan 30 menit sebelum setiap jadwal
- **Voice note** — kirim pesan suara, bot transkrip otomatis lalu proses
- **List & delete** — lihat semua reminder aktif, hapus by judul/nomor/semua
- **Duplikasi dicegah** — reminder identik tidak bisa masuk dua kali
- **Fallback AI** — jika Claude CLI tidak tersedia, otomatis pakai Ollama (lokal, gratis)

## Contoh Perintah

```
"Ingatkan meeting besok jam 9, catatan: bawa laptop"
"Ujian Rabu 3 Juni jam 8 ruang H.2.3 dan jam 13 ruang H.2.3"
"Tampilkan reminder"
"Hapus reminder meeting"
"Hapus semua"
"Hapus 1-5"
"Ubah reminder meeting jadi jam 3 sore"
"Kapan ujian berikutnya?"
```

Atau kirim **voice note** — bot akan transkrip dan proses otomatis.

## Stack

| Komponen | Teknologi |
|---|---|
| Language | Go 1.22+ |
| Bot | go-telegram-bot-api/v5 (long polling) |
| AI/NLU | Claude CLI (`claude -p`) → fallback Ollama |
| Database | SQLite via modernc.org/sqlite (pure Go) |
| Calendar | Google Calendar API v3 |
| Scheduler | robfig/cron/v3 |
| Transkripsi | OpenAI Whisper CLI |

## Prasyarat

- [Go 1.22+](https://go.dev/dl/)
- [Claude Code CLI](https://claude.ai/code) — sudah login
- [Ollama](https://ollama.com/download) + model (opsional, sebagai fallback)
- [Python + Whisper](https://github.com/openai/whisper) — untuk fitur voice note
- Telegram Bot Token dari [@BotFather](https://t.me/BotFather)
- Google Cloud project dengan Calendar API diaktifkan (opsional)

## Instalasi

### 1. Clone & download dependencies

```bash
git clone https://github.com/username/pf.git
cd pf
go mod tidy
```

### 2. Buat file `.env`

```env
TELEGRAM_BOT_TOKEN=your_token_here
TELEGRAM_CHAT_ID=your_chat_id_here
TIMEZONE=Asia/Jakarta
USE_GOOGLE_CALENDAR=false

# Ollama fallback (opsional)
OLLAMA_BASE_URL=http://localhost:11434
OLLAMA_MODEL=qwen2.5:3b

# Whisper voice note (opsional)
WHISPER_MODEL=small
```

> **Cara dapat Chat ID:** jalankan bot lalu kirim `/start` — bot akan balas dengan Chat ID kamu.

### 3. Google Calendar (opsional)

Lewati bagian ini jika tidak ingin pakai Google Calendar. Set `USE_GOOGLE_CALENDAR=false`.

1. Buka [Google Cloud Console](https://console.cloud.google.com)
2. Buat project baru → aktifkan **Google Calendar API**
3. Buat **OAuth 2.0 Client ID** (tipe: Desktop app)
4. Download JSON → simpan sebagai `credentials.json` di root project
5. Jalankan:
   ```bash
   go run ./cmd/auth
   ```
6. Buka URL yang muncul di browser, login, izinkan akses
7. Copy `code=...` dari URL redirect → paste ke terminal
8. `token.json` otomatis terbuat
9. Set `USE_GOOGLE_CALENDAR=true` di `.env`

### 4. Ollama fallback (opsional)

```bash
# Install Ollama dari https://ollama.com/download
ollama pull qwen2.5:3b
```

### 5. Whisper voice note (opsional)

```bash
pip install openai-whisper
```

### 6. Jalankan bot

```bash
go run ./cmd/bot
```

## Build Binary

```bash
# Linux/Mac
CGO_ENABLED=0 go build -o asisten-bot ./cmd/bot

# Windows (PowerShell)
$env:CGO_ENABLED=0; go build -o asisten-bot.exe ./cmd/bot
```

## Struktur Project

```
cmd/
  bot/main.go        # Entry point bot
  auth/main.go       # Helper OAuth Google Calendar (jalankan sekali)
internal/
  config/            # Load konfigurasi dari .env
  nlu/               # Parsing bahasa natural → struct (Claude CLI / Ollama)
  store/             # SQLite: CRUD reminder
  calendar/          # Google Calendar: create, update, delete event
  scheduler/         # Cron: notifikasi tepat waktu, H-30 menit, morning summary
  transcribe/        # Whisper: transkripsi voice note
```

## Environment Variables

| Variable | Default | Keterangan |
|---|---|---|
| `TELEGRAM_BOT_TOKEN` | — | Wajib. Token dari @BotFather |
| `TELEGRAM_CHAT_ID` | — | Wajib. Chat ID pemilik bot |
| `TIMEZONE` | `Asia/Jakarta` | Timezone untuk semua jadwal |
| `USE_GOOGLE_CALENDAR` | `false` | Aktifkan sync Google Calendar |
| `OLLAMA_BASE_URL` | `http://localhost:11434` | URL Ollama lokal |
| `OLLAMA_MODEL` | `llama3.2` | Model Ollama sebagai fallback |
| `WHISPER_MODEL` | `small` | Model Whisper: `base`/`small`/`medium` |

## Catatan

- Bot menggunakan **long polling**, bukan webhook — perlu proses yang berjalan terus (VPS/systemd)
- Database SQLite tersimpan di `data.db` di direktori yang sama dengan binary
- File `credentials.json`, `token.json`, `.env`, dan `data.db` **jangan di-commit** ke Git
