package store

import (
	"database/sql"
	"time"
)

type Note struct {
	ID        int64
	ChatID    string
	Content   string
	Tags      string
	CreatedAt time.Time
}

func (s *Store) AddNote(chatID, content, tags string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO notes (chat_id, content, tags) VALUES (?, ?, ?)`,
		chatID, content, nullStr(tags),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListNotes(chatID string) ([]Note, error) {
	rows, err := s.db.Query(
		`SELECT id, chat_id, content, COALESCE(tags,''), created_at
		 FROM notes WHERE chat_id=? ORDER BY created_at DESC`,
		chatID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotes(rows)
}

func (s *Store) SearchNotes(chatID, query string) ([]Note, error) {
	rows, err := s.db.Query(
		`SELECT id, chat_id, content, COALESCE(tags,''), created_at
		 FROM notes WHERE chat_id=? AND (LOWER(content) LIKE LOWER(?) OR LOWER(COALESCE(tags,'')) LIKE LOWER(?))
		 ORDER BY created_at DESC`,
		chatID, "%"+query+"%", "%"+query+"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotes(rows)
}

func (s *Store) DeleteNote(chatID, query string) (int, error) {
	res, err := s.db.Exec(
		`DELETE FROM notes WHERE chat_id=? AND (LOWER(content) LIKE LOWER(?) OR CAST(id AS TEXT)=?)`,
		chatID, "%"+query+"%", query,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func scanNotes(rows *sql.Rows) ([]Note, error) {
	var out []Note
	for rows.Next() {
		var n Note
		var created string
		if err := rows.Scan(&n.ID, &n.ChatID, &n.Content, &n.Tags, &created); err != nil {
			return nil, err
		}
		n.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
		out = append(out, n)
	}
	return out, rows.Err()
}
