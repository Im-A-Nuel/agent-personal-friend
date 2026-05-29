package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Reminder struct {
	ID          int64
	ChatID      string
	Title       string
	Description string
	RemindAt    time.Time
	Recurring   string
	CalendarID  string
	Notified    bool
}

type Store struct {
	db *sql.DB
}

func New(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn+"?_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS reminders (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		chat_id         TEXT NOT NULL,
		title           TEXT NOT NULL,
		remind_at       TEXT NOT NULL,
		recurring       TEXT DEFAULT NULL,
		calendar_id     TEXT DEFAULT NULL,
		notified        INTEGER DEFAULT 0,
		notified_early  INTEGER DEFAULT 0,
		created_at      TEXT DEFAULT (datetime('now'))
	)`)
	if err != nil {
		return err
	}
	_, _ = db.Exec(`ALTER TABLE reminders ADD COLUMN notified_early INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE reminders ADD COLUMN description TEXT DEFAULT NULL`)
	// Hapus duplikat lama sebelum buat unique index
	_, _ = db.Exec(`DELETE FROM reminders WHERE id NOT IN (
		SELECT MIN(id) FROM reminders GROUP BY chat_id, title, remind_at
	)`)
	_, _ = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_reminders_unique ON reminders(chat_id, title, remind_at)`)
	return nil
}

func (s *Store) AddReminder(chatID, title, description, remindAt, recurring, calendarID string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT OR IGNORE INTO reminders (chat_id, title, description, remind_at, recurring, calendar_id) VALUES (?, ?, ?, ?, ?, ?)`,
		chatID, title, nullStr(description), remindAt, nullStr(recurring), nullStr(calendarID),
	)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	rows, _ := res.RowsAffected()
	if rows == 0 {
		_ = s.db.QueryRow(
			`SELECT id FROM reminders WHERE chat_id=? AND title=? AND remind_at=?`,
			chatID, title, remindAt,
		).Scan(&id)
		return -id, nil // negatif = sinyal duplikat
	}
	return id, nil
}

func (s *Store) GetByID(chatID string, id int64) (*Reminder, error) {
	var r Reminder
	var remindAt string
	err := s.db.QueryRow(
		`SELECT id, chat_id, title, COALESCE(description,''), remind_at, COALESCE(recurring,''), COALESCE(calendar_id,'')
		 FROM reminders WHERE chat_id=? AND id=?`, chatID, id,
	).Scan(&r.ID, &r.ChatID, &r.Title, &r.Description, &remindAt, &r.Recurring, &r.CalendarID)
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339, remindAt)
	if err != nil {
		return nil, err
	}
	r.RemindAt = t
	return &r, nil
}

func (s *Store) UpdateReminder(id int64, title, description, remindAt, recurring, calendarID string) error {
	_, err := s.db.Exec(
		`UPDATE reminders SET title=?, description=?, remind_at=?, recurring=?, calendar_id=?, notified=0, notified_early=0 WHERE id=?`,
		title, nullStr(description), remindAt, nullStr(recurring), nullStr(calendarID), id,
	)
	return err
}

func (s *Store) GetDueReminders(now time.Time) ([]Reminder, error) {
	rows, err := s.db.Query(
		`SELECT id, chat_id, title, COALESCE(description,''), remind_at, COALESCE(recurring,''), COALESCE(calendar_id,'')
		 FROM reminders WHERE notified=0 AND remind_at <= ?`,
		now.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRemindersWithDesc(rows)
}

func (s *Store) MarkNotified(id int64) error {
	_, err := s.db.Exec(`UPDATE reminders SET notified=1 WHERE id=?`, id)
	return err
}

func (s *Store) RescheduleRecurring(id int64, next time.Time) error {
	_, err := s.db.Exec(
		`UPDATE reminders SET remind_at=?, notified=0 WHERE id=?`,
		next.UTC().Format(time.RFC3339), id,
	)
	return err
}

func (s *Store) ListUpcoming(chatID string) ([]Reminder, error) {
	rows, err := s.db.Query(
		`SELECT id, chat_id, title, COALESCE(description,''), remind_at, COALESCE(recurring,''), COALESCE(calendar_id,'')
		 FROM reminders WHERE chat_id=? AND notified=0 ORDER BY remind_at ASC`,
		chatID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRemindersWithDesc(rows)
}

func (s *Store) FindByQuery(chatID, query string) ([]Reminder, error) {
	rows, err := s.db.Query(
		`SELECT id, chat_id, title, COALESCE(description,''), remind_at, COALESCE(recurring,''), COALESCE(calendar_id,'')
		 FROM reminders WHERE chat_id=? AND notified=0 AND (LOWER(title) LIKE LOWER(?) OR CAST(id AS TEXT)=?)`,
		chatID, "%"+query+"%", query,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRemindersWithDesc(rows)
}

func (s *Store) FindByIDs(chatID string, ids []int64) ([]Reminder, error) {
	var out []Reminder
	for _, id := range ids {
		var r Reminder
		var remindAt string
		err := s.db.QueryRow(
			`SELECT id, chat_id, title, COALESCE(description,''), remind_at, COALESCE(recurring,''), COALESCE(calendar_id,'')
			 FROM reminders WHERE chat_id=? AND id=?`, chatID, id,
		).Scan(&r.ID, &r.ChatID, &r.Title, &r.Description, &remindAt, &r.Recurring, &r.CalendarID)
		if err != nil {
			continue
		}
		t, _ := time.Parse(time.RFC3339, remindAt)
		r.RemindAt = t
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) DeleteReminder(chatID string, query string) (int, error) {
	res, err := s.db.Exec(
		`DELETE FROM reminders WHERE chat_id=? AND (LOWER(title) LIKE LOWER(?) OR CAST(id AS TEXT)=?)`,
		chatID, "%"+query+"%", query,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *Store) DeleteAll(chatID string) (int, error) {
	res, err := s.db.Exec(`DELETE FROM reminders WHERE chat_id=?`, chatID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *Store) DeleteByIDs(chatID string, ids []int64) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	total := 0
	for _, id := range ids {
		res, err := s.db.Exec(`DELETE FROM reminders WHERE chat_id=? AND id=?`, chatID, id)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += int(n)
	}
	return total, nil
}

func (s *Store) MarkEarlyNotified(id int64) error {
	_, err := s.db.Exec(`UPDATE reminders SET notified_early=1 WHERE id=?`, id)
	return err
}

// GetEarlyReminders returns reminders due within warningMinutes that haven't been early-notified yet.
func (s *Store) GetEarlyReminders(now time.Time, warningMinutes int) ([]Reminder, error) {
	target := now.Add(time.Duration(warningMinutes) * time.Minute)
	rows, err := s.db.Query(
		`SELECT id, chat_id, title, COALESCE(description,''), remind_at, COALESCE(recurring,''), COALESCE(calendar_id,'')
		 FROM reminders WHERE notified=0 AND notified_early=0
		   AND remind_at > ? AND remind_at <= ?`,
		now.UTC().Format(time.RFC3339),
		target.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRemindersWithDesc(rows)
}

func (s *Store) GetTodayReminders(chatID string, day time.Time) ([]Reminder, error) {
	loc := day.Location()
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	rows, err := s.db.Query(
		`SELECT id, chat_id, title, COALESCE(description,''), remind_at, COALESCE(recurring,''), COALESCE(calendar_id,'')
		 FROM reminders WHERE chat_id=? AND notified=0
		   AND remind_at >= ? AND remind_at < ? ORDER BY remind_at ASC`,
		chatID,
		start.UTC().Format(time.RFC3339),
		end.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRemindersWithDesc(rows)
}

func (s *Store) Close() error {
	return s.db.Close()
}

func scanReminders(rows *sql.Rows) ([]Reminder, error) {
	var out []Reminder
	for rows.Next() {
		var r Reminder
		var remindAt string
		if err := rows.Scan(&r.ID, &r.ChatID, &r.Title, &remindAt, &r.Recurring, &r.CalendarID); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, remindAt)
		if err != nil {
			return nil, fmt.Errorf("parse remind_at %q: %w", remindAt, err)
		}
		r.RemindAt = t
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanRemindersWithDesc(rows *sql.Rows) ([]Reminder, error) {
	var out []Reminder
	for rows.Next() {
		var r Reminder
		var remindAt string
		if err := rows.Scan(&r.ID, &r.ChatID, &r.Title, &r.Description, &remindAt, &r.Recurring, &r.CalendarID); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, remindAt)
		if err != nil {
			return nil, fmt.Errorf("parse remind_at %q: %w", remindAt, err)
		}
		r.RemindAt = t
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
