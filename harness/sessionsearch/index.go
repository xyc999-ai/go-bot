// Package sessionsearch indexes the visible conversation from committed v4 events.
package sessionsearch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go-bot/harness/session"

	_ "modernc.org/sqlite"
)

// Source is bound to one account by the Host. Snapshot returns the committed
// suffix starting at next, or a reset prefix if the previous cursor is stale.
// More requests another bounded page before sync/query is considered complete.
type Source struct {
	List     func(context.Context, string, int) (sessionformat.SessionListPage, error)
	Snapshot func(context.Context, string, Cursor) (Snapshot, error)
	Title    func(string) string
}

type Cursor struct {
	// Partial is an in-process continuation; interrupted page sequences are
	// persisted with Size=-1 and safely rebuilt on a later cold sync.
	Partial bool
	Next    int
	Size    int64
	ModTime int64
}

type Snapshot struct {
	More bool
	Cursor
	Events []sessionformat.Event
	Reset  bool
}

type Index struct {
	db     *sql.DB
	source Source
	mu     sync.Mutex
}

const schema = `
CREATE TABLE IF NOT EXISTS owner (account TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY, created INTEGER NOT NULL,
    next INTEGER NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS messages (
    id INTEGER PRIMARY KEY, session_id TEXT NOT NULL, seq INTEGER NOT NULL,
    time INTEGER NOT NULL, role TEXT NOT NULL, body TEXT NOT NULL,
    UNIQUE(session_id, seq)
);
CREATE VIRTUAL TABLE IF NOT EXISTS message_fts USING fts5(body, tokenize='unicode61');
`

func Open(ctx context.Context, path, account string, source Source) (*Index, error) {
	if account == "" || source.List == nil || source.Snapshot == nil {
		return nil, errors.New("session search requires an account and history source")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	index := &Index{db: db, source: source}
	if err := index.initialize(ctx, account); err != nil {
		_ = db.Close()
		return nil, err
	}
	return index, nil
}

func (i *Index) initialize(ctx context.Context, account string) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, schema); err != nil {
		return err
	}
	var owner string
	err = tx.QueryRowContext(ctx, "SELECT account FROM owner").Scan(&owner)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, "INSERT INTO owner VALUES (?)", account)
	case err == nil && owner != account:
		_, err = tx.ExecContext(ctx, "DELETE FROM message_fts; DELETE FROM messages; DELETE FROM sessions;")
		if err == nil {
			_, err = tx.ExecContext(ctx, "UPDATE owner SET account = ?", account)
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (i *Index) Close() error { return i.db.Close() }

func (i *Index) Sync(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.sync(ctx)
}

func (i *Index) sync(ctx context.Context) error {
	cursors := make(map[string]Cursor)
	rows, err := i.db.QueryContext(ctx, "SELECT id, next, size, mtime FROM sessions")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var cursor Cursor
		if err := rows.Scan(&id, &cursor.Next, &cursor.Size, &cursor.ModTime); err != nil {
			_ = rows.Close()
			return err
		}
		cursors[id] = cursor
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for after := ""; ; {
		page, err := i.source.List(ctx, after, 100)
		if err != nil {
			return err
		}
		for _, header := range page.Sessions {
			cursor := cursors[header.ID]
			delete(cursors, header.ID)
			for {
				snapshot, err := i.source.Snapshot(ctx, header.ID, cursor)
				if errors.Is(err, os.ErrNotExist) {
					if err := i.dropSession(ctx, header.ID); err != nil {
						return err
					}
					break
				}
				if err != nil {
					return fmt.Errorf("read session %s for indexing: %w", header.ID, err)
				}
				if snapshot.More && (len(snapshot.Events) == 0 || !snapshot.Reset && snapshot.Next <= cursor.Next) {
					return errors.New("session history page made no progress")
				}
				if snapshot.Reset || snapshot.Cursor != cursor || len(snapshot.Events) != 0 || cursor == (Cursor{}) {
					if err := i.save(ctx, header, snapshot); err != nil {
						return err
					}
				}
				if !snapshot.More {
					break
				}
				cursor = snapshot.Cursor
				cursor.Partial = true
			}

		}
		if page.NextCursor == "" {
			break
		}
		after = page.NextCursor
	}
	for id := range cursors {
		if err := i.dropSession(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (i *Index) save(ctx context.Context, header sessionformat.Header, snapshot Snapshot) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if snapshot.Reset {
		if err := deleteMessages(ctx, tx, header.ID); err != nil {
			return err
		}
	}
	for _, event := range snapshot.Events {
		role, body := visibleText(event)
		if body == "" {
			continue
		}
		result, err := tx.ExecContext(ctx, "INSERT INTO messages(session_id, seq, time, role, body) VALUES (?, ?, ?, ?, ?)",
			header.ID, event.Seq, event.Time, role, body)
		if err != nil {
			return err
		}
		rowID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO message_fts(rowid, body) VALUES (?, ?)", rowID, segmentHan(body)); err != nil {
			return err
		}
	}
	storedSize := snapshot.Size
	if snapshot.More {
		storedSize = -1
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET next=excluded.next, size=excluded.size, mtime=excluded.mtime`,
		header.ID, header.CreatedAt, snapshot.Next, storedSize, snapshot.ModTime)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (i *Index) dropSession(ctx context.Context, id string) error {
	tx, err := i.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteMessages(ctx, tx, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sessions WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteMessages(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM message_fts WHERE rowid IN (SELECT id FROM messages WHERE session_id = ?)", id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM messages WHERE session_id = ?", id)
	return err
}

func visibleText(event sessionformat.Event) (string, string) {
	raw := event.Data
	var role string
	switch event.Type {
	case "user/message":
		role = "user"
	case "assistant/message":
		role = "assistant"
		var data struct{ Message json.RawMessage }
		if json.Unmarshal(raw, &data) != nil {
			return "", ""
		}
		raw = data.Message
	default:
		return "", ""
	}
	var message struct {
		Source  struct{ Kind string }
		Content []struct{ Type, Text string }
	}
	if json.Unmarshal(raw, &message) != nil || (role == "user" && message.Source.Kind != "user") || message.Source.Kind == "go-bot/welcome" {
		return "", ""
	}
	var blocks []string
	for _, block := range message.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			blocks = append(blocks, block.Text)
		}
	}
	return role, strings.Join(blocks, "\n")
}
