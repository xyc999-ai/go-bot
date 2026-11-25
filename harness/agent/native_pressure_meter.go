package agent

import (
	"encoding/json"

	"go-bot/harness/session"
)

// nativePressureMeter follows only committed v4 events. In particular, the
// provider sample is tied to the surface immediately before its assistant
// event, not the later surface after tools, pruning or compaction.
type nativePressureMeter struct {
	cursor  int
	surface *sessionformat.SurfaceState
	header  any
	anchor  *nativePressureAnchor
}

type nativePressureAnchor struct {
	header  any
	before  sessionformat.Surface
	message json.RawMessage
	usage   nativePressureUsage
}

type nativePressureUsage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
}

func (a *NativeLoop) latestNativePressureAnchor() (*nativePressureAnchor, error) {
	if a.pressureMeter == nil {
		surface, err := sessionformat.NewSurfaceState(nil)
		if err != nil {
			return nil, err
		}
		a.pressureMeter = &nativePressureMeter{surface: surface}
	}
	meter := a.pressureMeter
	events, err := a.session.DurableEventsFrom(meter.cursor)
	if err != nil {
		a.pressureMeter = nil
		return nil, err
	}
	for _, event := range events {
		switch event.Type {
		case "request/header":
			var record struct {
				Header any `json:"header"`
			}
			if err := json.Unmarshal(event.Data, &record); err != nil {
				a.pressureMeter = nil
				return nil, err
			}
			meter.header = record.Header
		case "assistant/message":
			var call struct {
				Message json.RawMessage      `json:"message"`
				Usage   *nativePressureUsage `json:"usage"`
			}
			if err := json.Unmarshal(event.Data, &call); err != nil {
				a.pressureMeter = nil
				return nil, err
			}
			// A later assistant without usage supersedes the previous sample.
			meter.anchor = nil
			if call.Usage != nil {
				before, err := meter.surface.ModelSnapshot()
				if err != nil {
					a.pressureMeter = nil
					return nil, err
				}
				meter.anchor = &nativePressureAnchor{header: meter.header, before: before, message: call.Message, usage: *call.Usage}
			}
		}
		if err := meter.surface.Append(event); err != nil {
			a.pressureMeter = nil
			return nil, err
		}
		meter.cursor++
	}
	return meter.anchor, nil
}
