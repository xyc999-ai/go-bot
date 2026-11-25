package sessionformat

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

var ErrHistoryChanged = errors.New("Session history changed during read")

// FileHistoryReader retains offsets and small derived state, never full event
// bodies. Each file version is strictly inspected once; warm reads seek to the
// requested window. A changed inode, size or mtime invalidates the observation.
// Initial inspection remains a full read, including recoverable-tail policy.
type FileHistoryReader struct {
	Configure  func(*LiveSession) error
	MaxEntries int
	mu         sync.Mutex
	clock      uint64
	entries    map[string]*fileHistoryEntry
}
type fileHistoryEntry struct {
	mu          sync.Mutex
	used        uint64 // protected by reader.mu
	info        os.FileInfo
	offsets     []int64
	turns       []uint64
	control     HistoryWindow
	attachments map[string][]HistoricalAttachment
}

func sameHistoryFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
func (r *FileHistoryReader) entry(path string) *fileHistoryEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]*fileHistoryEntry)
	}
	e := r.entries[path]
	if e == nil {
		e = &fileHistoryEntry{}
		r.entries[path] = e
	}
	r.clock++
	e.used = r.clock
	capacity := r.MaxEntries
	if capacity < 1 {
		capacity = 8
	}
	for len(r.entries) > capacity {
		old := ""
		var used uint64
		for key, value := range r.entries {
			if key == path {
				continue
			}
			if old == "" || value.used < used {
				old, used = key, value.used
			}
		}
		delete(r.entries, old)
	}
	return e
}
func (r *FileHistoryReader) Read(ctx context.Context, store FileStore, id string, request HistoryRequest) (HistoryWindow, error) {
	path, err := store.Path(id)
	if err != nil {
		return HistoryWindow{}, err
	}
	e := r.entry(path)
	e.mu.Lock()
	defer e.mu.Unlock()
	for range 3 {
		if err := ctx.Err(); err != nil {
			return HistoryWindow{}, err
		}
		value, err := r.readOnce(ctx, e, store, id, path, request)
		if !errors.Is(err, ErrHistoryChanged) {
			return value, err
		}
		e.info = nil
	}
	return HistoryWindow{}, ErrHistoryChanged
}
func (r *FileHistoryReader) readOnce(ctx context.Context, e *fileHistoryEntry, store FileStore, id, path string, request HistoryRequest) (HistoryWindow, error) {
	file, err := os.Open(path)
	if err != nil {
		return HistoryWindow{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return HistoryWindow{}, err
	}
	if !sameHistoryFile(info, e.info) {
		artifact, _, err := store.readArtifact(file, id)
		if err != nil {
			return HistoryWindow{}, err
		}
		state, err := replaySession(artifact, nil, len(artifact.Events))
		if err != nil {
			return HistoryWindow{}, err
		}
		if r.Configure != nil {
			if err := r.Configure(state); err != nil {
				return HistoryWindow{}, err
			}
		}
		// This sealed observation will never append. Live and durable describe the
		// same inspected prefix, so reuse the already reconstructed cells.
		for _, cell := range state.projections {
			cell.durable = cell.live
		}
		control, runtime, err := state.historyStateLocked()
		if err != nil {
			return HistoryWindow{}, err
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return HistoryWindow{}, err
		}
		scanner := bufio.NewReader(file)
		header, err := scanner.ReadBytes('\n')
		if err != nil {
			return HistoryWindow{}, err
		}
		offsets := make([]int64, len(artifact.Events)+1)
		offsets[0] = int64(len(header))
		for i := range artifact.Events {
			if err := ctx.Err(); err != nil {
				return HistoryWindow{}, err
			}
			line, err := scanner.ReadBytes('\n')
			if err != nil {
				return HistoryWindow{}, err
			}
			offsets[i+1] = offsets[i] + int64(len(line))
		}
		after, err := file.Stat()
		if err != nil {
			return HistoryWindow{}, err
		}
		if !sameHistoryFile(info, after) {
			return HistoryWindow{}, ErrHistoryChanged
		}
		e.info, e.offsets, e.turns, e.control, e.attachments = info, offsets, runtime.TurnStarts, control, runtime.Attachments
	}
	if err := store.validateHeader(e.control.Header); err != nil {
		return HistoryWindow{}, err
	}
	from, to, before, err := historyRange(request, e.control.Count, e.turns)
	if err != nil {
		return HistoryWindow{}, err
	}
	value := HistoryWindow{Header: e.control.Header, Count: e.control.Count, InheritedEventCount: e.control.InheritedEventCount}
	if request.Kind != HistoryAttachment {
		value = cloneHistoryState(e.control)
	}
	value.Before = before
	value.Events = make([]Event, 0, to-from)
	if request.Kind == HistoryAttachment {
		value.Attachment = (runtimeProjection{Attachments: e.attachments}).attachment(request.AttachmentID, request.AttachmentName)
	}
	if from < to {
		lines := bufio.NewReader(io.NewSectionReader(file, e.offsets[from], e.offsets[to]-e.offsets[from]))
		for seq := from; seq < to; seq++ {
			if err := ctx.Err(); err != nil {
				return HistoryWindow{}, err
			}
			line, err := lines.ReadBytes('\n')
			if err != nil {
				return HistoryWindow{}, err
			}
			event, err := DecodeEvent(line[:len(line)-1], seq)
			if err != nil {
				return HistoryWindow{}, fmt.Errorf("read indexed event %d: %w", seq, err)
			}
			value.Events = append(value.Events, event)
		}
	}
	final, err := file.Stat()
	if err != nil {
		return HistoryWindow{}, err
	}
	current, err := os.Stat(path)
	if err != nil {
		return HistoryWindow{}, err
	}
	if !sameHistoryFile(e.info, final) || !sameHistoryFile(e.info, current) {
		return HistoryWindow{}, ErrHistoryChanged
	}
	return value, nil
}

// HistoryFromArtifact supports read-only adapters without a file window backend.
// It validates and folds a supplied artifact; production file readers use the
// retained offset index above instead of invoking this for each page.
func HistoryFromArtifact(ctx context.Context, artifact PhysicalArtifact, request HistoryRequest, configure func(*LiveSession) error) (HistoryWindow, error) {
	s, err := replaySession(artifact, nil, len(artifact.Events))
	if err != nil {
		return HistoryWindow{}, err
	}
	if configure != nil {
		if err := configure(s); err != nil {
			return HistoryWindow{}, err
		}
	}
	for _, cell := range s.projections {
		cell.durable = cell.live
	}
	return s.HistoryWindow(ctx, request)
}
