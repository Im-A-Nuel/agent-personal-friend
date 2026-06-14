package store

import (
	"database/sql"
	"time"
)

type Task struct {
	ID       int64
	ChatID   string
	Title    string
	Priority string // low | normal | high
	Done     bool
	Due      *time.Time // nil jika tidak ada deadline
}

func (s *Store) AddTask(chatID, title, priority, due string) (int64, error) {
	if priority == "" {
		priority = "normal"
	}
	res, err := s.db.Exec(
		`INSERT INTO tasks (chat_id, title, priority, due) VALUES (?, ?, ?, ?)`,
		chatID, title, priority, nullStr(due),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListTasks returns tasks for a chat. includeDone=false → hanya pending.
func (s *Store) ListTasks(chatID string, includeDone bool) ([]Task, error) {
	q := `SELECT id, chat_id, title, priority, done, COALESCE(due,'') FROM tasks WHERE chat_id=?`
	if !includeDone {
		q += ` AND done=0`
	}
	// urut: prioritas tinggi dulu, lalu deadline terdekat
	q += ` ORDER BY CASE priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END, due IS NULL, due ASC`
	rows, err := s.db.Query(q, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTasks(rows)
}

func (s *Store) MarkTaskDone(chatID, query string) (int, error) {
	res, err := s.db.Exec(
		`UPDATE tasks SET done=1 WHERE chat_id=? AND done=0 AND (LOWER(title) LIKE LOWER(?) OR CAST(id AS TEXT)=?)`,
		chatID, "%"+query+"%", query,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (s *Store) DeleteTask(chatID, query string) (int, error) {
	res, err := s.db.Exec(
		`DELETE FROM tasks WHERE chat_id=? AND (LOWER(title) LIKE LOWER(?) OR CAST(id AS TEXT)=?)`,
		chatID, "%"+query+"%", query,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func scanTasks(rows *sql.Rows) ([]Task, error) {
	var out []Task
	for rows.Next() {
		var t Task
		var due string
		if err := rows.Scan(&t.ID, &t.ChatID, &t.Title, &t.Priority, &t.Done, &due); err != nil {
			return nil, err
		}
		if due != "" {
			if parsed, err := time.Parse(time.RFC3339, due); err == nil {
				t.Due = &parsed
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
