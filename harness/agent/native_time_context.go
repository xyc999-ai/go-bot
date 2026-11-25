package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go-bot/harness/session"
)

// nativeTimeContext mirrors DeepSeek's opt-in, pre-step clock snapshot. Keep
// time out of the system prompt so it does not go stale in a long-lived Host.
type nativeTimeContext struct {
	zone     string
	location *time.Location
	refresh  time.Duration
}

// EnableTimeContext installs the clock only for Agents whose composition
// includes Schedule. A zero refresh injects on every step, as upstream does.
func (a *NativeLoop) EnableTimeContext(zone string, refresh time.Duration) error {
	if refresh < 0 {
		return errors.New("time-context refresh must not be negative")
	}
	if zone == "" {
		return errors.New("time-context zone is required")
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return fmt.Errorf("invalid time-context zone %q: %w", zone, err)
	}
	a.timeContext = &nativeTimeContext{zone: zone, location: location, refresh: refresh}
	return nil
}

func (a *NativeLoop) ensureTimeContext(ctx context.Context, turn uint64, step int) error {
	clock := a.timeContext
	if clock == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now()
	state := a.session.TimeContext()
	lastMessage, lastInjection, lastTurnInjection := state.BeforeStepMessage, state.LastInjection, state.LastTurnInjection
	hasMessage, hasInjection, hasTurnInjection := state.HasBeforeStepMessage, state.HasInjection, state.HasTurnInjection
	browserZones := make(map[string]struct{}, len(state.BrowserTimeZones))
	for _, zone := range state.BrowserTimeZones {
		browserZones[zone] = struct{}{}
	}
	if clock.refresh > 0 && hasInjection && now.UnixMilli() >= lastInjection &&
		now.Sub(time.UnixMilli(lastInjection)) < clock.refresh {
		return nil
	}
	previous := int64(0)
	available := false
	baseline := "model-visible message"
	if step == 1 {
		previous, available = lastMessage, hasMessage
	} else {
		previous, available = lastTurnInjection, hasTurnInjection
		baseline = "step context"
	}
	elapsed := "unavailable"
	if available {
		elapsed = nativeTimeDuration(now.Sub(time.UnixMilli(previous)))
	}
	message, err := sampleNativeTimeContext(clock, now, turn, step, baseline, elapsed, browserZones)
	if err != nil {
		return err
	}
	_, err = a.append("user/message", message, json.RawMessage(`"append"`), nil)
	return err
}

// SampleTimeContext produces the same standard clock snapshot as the normal
// pre-step refresh. An external owner may inject it through the existing Inbox
// before driving an idle Agent. It does not append a log row or start a turn.
func (a *NativeLoop) SampleTimeContext() (json.RawMessage, error) {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.timeContext == nil {
		return nil, errors.New("time-context is not enabled for this Agent")
	}
	now := time.Now()
	turn := a.session.TurnBoundary(sessionformat.LiveCut).LastTurn + 1
	state := a.session.TimeContext()
	previous, available := state.LastMessage, state.HasMessage
	elapsed := "unavailable"
	if available {
		elapsed = nativeTimeDuration(now.Sub(time.UnixMilli(previous)))
	}
	return sampleNativeTimeContext(a.timeContext, now, turn, 1, "model-visible message", elapsed, nil)
}

func sampleNativeTimeContext(clock *nativeTimeContext, now time.Time, turn uint64, step int, baseline, elapsed string, browserZones map[string]struct{}) (json.RawMessage, error) {
	selected := *clock
	browserText := "Browser time zone for this request: unavailable. Ask the user to clarify otherwise-unqualified dates and times."
	if len(browserZones) == 1 {
		for zone := range browserZones {
			location, err := time.LoadLocation(zone)
			if err != nil {
				return nil, fmt.Errorf("invalid browser time zone %q: %w", zone, err)
			}
			selected.zone, selected.location = zone, location
			browserText = fmt.Sprintf("Browser time zone for this request: %s. Interpret otherwise-unqualified dates and times in this zone.", zone)
		}
	} else if len(browserZones) > 1 {
		zones := make([]string, 0, len(browserZones))
		for zone := range browserZones {
			zones = append(zones, zone)
		}
		slices.Sort(zones)
		encoded, err := json.Marshal(zones)
		if err != nil {
			return nil, err
		}
		browserText = fmt.Sprintf("Browser time zone for this request: mixed %s. Ask the user to clarify otherwise-unqualified dates and times.", encoded)
	}
	text := fmt.Sprintf("Time sampled while preparing turn %d, step %d: %s\n", turn, step,
		nativeTimeTimestamp(now, &selected)) +
		browserText + "\n" +
		fmt.Sprintf("Elapsed since the preceding %s: %s.", baseline, elapsed)
	message := map[string]any{
		"id": uuid.NewString(), "role": "user",
		"source":  map[string]any{"kind": "time-context", "form": "snapshot", "sections": []any{map[string]any{"name": "time-context", "text": text}}},
		"content": []any{map[string]any{"type": "text", "text": text}},
	}
	return json.Marshal(message)
}

func nativeTimeTimestamp(now time.Time, clock *nativeTimeContext) string {
	local := now.In(clock.location)
	_, secondsEast := local.Zone()
	sign := "+"
	if secondsEast < 0 {
		sign = "-"
		secondsEast = -secondsEast
	}
	return fmt.Sprintf("%s%s%02d:%02d[%s]", local.Format("2006-01-02T15:04:05"), sign,
		secondsEast/3600, secondsEast%3600/60, clock.zone)
}

func nativeTimeDuration(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	seconds := int64(elapsed / time.Second)
	days, hours := seconds/86400, seconds%86400/3600
	minutes, rest := seconds%3600/60, seconds%60
	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	parts = append(parts, fmt.Sprintf("%ds", rest))
	return strings.Join(parts, " ")
}
