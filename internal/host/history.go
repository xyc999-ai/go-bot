package host

import (
	"context"
	"os"

	"go-bot/harness/session"
	"go-bot/harness/sessionsearch"
	"go-bot/internal/hostruntime"
)

func historySnapshot(host *hostruntime.NativeHost) func(context.Context, string, sessionsearch.Cursor) (sessionsearch.Snapshot, error) {
	return func(ctx context.Context, id string, cursor sessionsearch.Cursor) (snapshot sessionsearch.Snapshot, err error) {
		err = host.WithSessionHistory(ctx, id, func() error {
			snapshot, err = readHistorySnapshot(ctx, host, id, cursor)
			return err
		})
		return snapshot, err
	}
}

func readHistorySnapshot(ctx context.Context, host *hostruntime.NativeHost, id string, cursor sessionsearch.Cursor) (sessionsearch.Snapshot, error) {
	archived, err := host.Archived(id)
	if err != nil {
		return sessionsearch.Snapshot{}, err
	}
	if archived {
		return sessionsearch.Snapshot{}, os.ErrNotExist
	}
	if host.ReadHistory != nil {
		var after *uint64
		if cursor.Next > 0 {
			v := uint64(cursor.Next - 1)
			after = &v
		}
		value, found, err := host.DurableSessionPage(ctx, id, sessionformat.HistoryRequest{Kind: sessionformat.HistoryEvents, After: after, Limit: 500})
		if found && err == nil {
			reset := uint64(cursor.Next) > value.Count
			if reset {
				value, _, err = host.DurableSessionPage(ctx, id, sessionformat.HistoryRequest{Kind: sessionformat.HistoryEvents, Limit: 500})
				if err != nil {
					return sessionsearch.Snapshot{}, err
				}
			}
			next := cursor.Next + len(value.Events)
			if reset {
				next = len(value.Events)
			}
			more := uint64(next) < value.Count
			return sessionsearch.Snapshot{Cursor: sessionsearch.Cursor{Next: next, Size: -1, Partial: more}, More: more, Reset: reset, Events: value.Events}, nil
		}
	}

	if host.ReadHistory == nil {
		if events, reset, found, err := host.DurableSessionSuffix(id, cursor.Next); found {
			if err == nil {
				next := cursor.Next + len(events)
				if reset {
					next = len(events)
				}
				// A live snapshot is based on the fsync watermark rather than stat.
				return sessionsearch.Snapshot{Cursor: sessionsearch.Cursor{Next: next, Size: -1}, Events: events, Reset: reset}, nil
			}
			// Session shutdown may have detached its live journal during lookup.
		}
	}

	info, err := host.SessionLogInfo(id)
	if err != nil {
		return sessionsearch.Snapshot{}, err
	}
	snapshot := sessionsearch.Snapshot{Cursor: sessionsearch.Cursor{Next: cursor.Next, Size: info.Size(), ModTime: info.ModTime().UnixNano()}}
	if snapshot.Cursor == cursor {
		return snapshot, nil
	}
	if host.ReadHistory != nil {
		next := cursor.Next
		sameVersion := info.Size() == cursor.Size && info.ModTime().UnixNano() == cursor.ModTime
		if cursor.Size < 0 || !sameVersion && info.Size() <= cursor.Size {
			next = 0
			snapshot.Reset = true
		}
		var after *uint64
		if next > 0 {
			v := uint64(next - 1)
			after = &v
		}
		value, err := host.ReadHistory(ctx, id, sessionformat.HistoryRequest{Kind: sessionformat.HistoryEvents, After: after, Limit: 500})
		if err != nil {
			return snapshot, err
		}
		if uint64(next) > value.Count {
			next = 0
			snapshot.Reset = true
			value, err = host.ReadHistory(ctx, id, sessionformat.HistoryRequest{Kind: sessionformat.HistoryEvents, Limit: 500})
			if err != nil {
				return snapshot, err
			}
		}
		snapshot.Events = value.Events
		snapshot.Next = next + len(value.Events)
		snapshot.More = uint64(snapshot.Next) < value.Count
		snapshot.Partial = snapshot.More
		return snapshot, nil
	}

	artifact, err := host.ReadSession(ctx, id)
	if err != nil {
		return sessionsearch.Snapshot{}, err
	}
	next := cursor.Next
	if cursor.Size < 0 || info.Size() <= cursor.Size || next > len(artifact.Events) {
		next = 0
		snapshot.Reset = true
	}
	snapshot.Next = len(artifact.Events)
	snapshot.Events = artifact.Events[next:]
	return snapshot, nil
}
