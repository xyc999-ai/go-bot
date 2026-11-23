package sessionsearch

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Activity is a bounded, source-addressable view of real visible dialogue.
// It reuses the committed-message index; it neither creates another history
// store nor changes session_search's existing created-at query semantics.
type Activity struct {
	Since     int64             `json:"since"`
	Sessions  []ActivitySession `json:"sessions"`
	Truncated bool              `json:"truncated"`
}

type ActivitySession struct {
	SessionID string    `json:"sessionId"`
	Title     string    `json:"title,omitempty"`
	UpdatedAt int64     `json:"updatedAt"`
	Messages  []Message `json:"messages"`
}

func (i *Index) RecentActivity(ctx context.Context, main string, now time.Time) (Activity, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	result := Activity{Since: now.Add(-7 * 24 * time.Hour).UnixMilli(), Sessions: []ActivitySession{}}
	if err := i.sync(ctx); err != nil {
		return result, err
	}
	// Activity is MAX(message.time), never Session.created or journal mtime.
	rows, err := i.db.QueryContext(ctx, `SELECT session_id, MAX(time) FROM messages
WHERE time >= ? AND session_id <> ? GROUP BY session_id ORDER BY MAX(time) DESC, session_id LIMIT 9`, result.Since, main)
	if err != nil {
		return result, err
	}
	var selected []ActivitySession
	for rows.Next() {
		var entry ActivitySession
		if err := rows.Scan(&entry.SessionID, &entry.UpdatedAt); err != nil {
			_ = rows.Close()
			return result, err
		}
		selected = append(selected, entry)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return result, err
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	if len(selected) > 8 {
		selected, result.Truncated = selected[:8], true
	}
	if main != "" {
		var last sql.NullInt64
		if err := i.db.QueryRowContext(ctx, `SELECT MAX(time) FROM messages WHERE session_id=? AND time>=?`, main, result.Since).Scan(&last); err != nil {
			return result, err
		}
		if last.Valid {
			selected = append([]ActivitySession{{SessionID: main, UpdatedAt: last.Int64}}, selected...)
		}
	}
	budget := 30000
	for _, entry := range selected {
		if i.source.Title != nil {
			entry.Title = i.source.Title(entry.SessionID)
		}
		messages, err := i.recentDialogue(ctx, entry.SessionID, result.Since)
		if err != nil {
			return result, err
		}
		for _, message := range messages {
			if budget == 0 {
				result.Truncated = true
				break
			}
			chars := []rune(message.Content)
			if len(chars) > min(1000, budget) {
				chars, message.Truncated = chars[:min(1000, budget)], true
			}
			message.Content = string(chars)
			budget -= len(chars)
			entry.Messages = append(entry.Messages, message)
		}
		if len(entry.Messages) != 0 {
			result.Sessions = append(result.Sessions, entry)
		}
	}
	return result, nil
}

func (i *Index) recentDialogue(ctx context.Context, id string, since int64) ([]Message, error) {
	// Keep the last four actual user inputs and the latest assistant dialogue
	// belonging to each. Text around tools can span several messages; at most
	// two assistant messages per exchange keep this preview bounded. Earlier
	// details remain retrievable by the original seq anchors.
	rows, err := i.db.QueryContext(ctx, `WITH last_users AS (
 SELECT seq FROM messages WHERE session_id=? AND time>=? AND role='user' ORDER BY seq DESC LIMIT 4
), recent AS (
 SELECT seq, role, body, time, MAX(CASE WHEN role='user' THEN seq END) OVER (ORDER BY seq) AS exchange
 FROM messages WHERE session_id=? AND time>=? AND seq >= COALESCE((SELECT MIN(seq) FROM last_users), 0)
), ranked AS (
 SELECT *, ROW_NUMBER() OVER (PARTITION BY exchange, role ORDER BY seq DESC) AS rank FROM recent
)
SELECT seq, role, SUBSTR(body,1,1001), time, LENGTH(body)>1000 FROM ranked WHERE role='user' OR rank<=2 ORDER BY seq`, id, since, id, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Message
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.ID, &message.Role, &message.Content, &message.Timestamp, &message.Truncated); err != nil {
			return nil, err
		}
		result = append(result, message)
	}
	return result, rows.Err()
}

// LatestActivityAt is useful to observe committed dialogue without treating a
// front-end reconnect or log mtime as new user information.
func (i *Index) LatestActivityAt(ctx context.Context) (int64, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if err := i.sync(ctx); err != nil {
		return 0, err
	}
	var at sql.NullInt64
	err := i.db.QueryRowContext(ctx, `SELECT MAX(time) FROM messages`).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return at.Int64, err
}
