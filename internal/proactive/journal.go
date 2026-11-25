package proactive

import (
	"encoding/json"
	"time"

	sessionformat "go-bot/harness/session"
)

// CommittedResults folds existing v4 facts only. An assistant's advertised
// arguments, an unsuccessful tool/result, or an interrupted turn is not a
// completed proactive decision. No extra result or notification log is needed.
type resultProjection struct {
	turn      uint64
	candidate *Result
	intents   map[string]Result
	results   []Completed
}

func newResultProjection() resultProjection {
	return resultProjection{intents: make(map[string]Result)}
}
func nextResult(state resultProjection, event sessionformat.Event) (resultProjection, error) {
	switch event.Type {
	case "turn/start":
		var data struct{ Turn uint64 }
		if json.Unmarshal(event.Data, &data) == nil {
			state.turn = data.Turn
		}
		state.candidate = nil
		clear(state.intents)
	case "tool/call":
		var data struct {
			Turn      uint64 `json:"turn"`
			CallID    string `json:"callId"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if json.Unmarshal(event.Data, &data) != nil || state.turn == 0 || data.Turn != state.turn || data.Name != "structured_output" {
			return state, nil
		}
		if result, err := DecodeResult(json.RawMessage(data.Arguments), time.UnixMilli(event.Time)); err == nil {
			state.intents[data.CallID] = result
		}
	case "tool/result":
		var data struct {
			Turn    uint64 `json:"turn"`
			Message struct {
				CallID  string `json:"toolCallId"`
				IsError bool   `json:"isError"`
			} `json:"message"`
		}
		if json.Unmarshal(event.Data, &data) != nil || data.Turn != state.turn || state.turn == 0 {
			return state, nil
		}
		if result, ok := state.intents[data.Message.CallID]; ok && !data.Message.IsError {
			copy := result
			state.candidate = &copy
		}
		delete(state.intents, data.Message.CallID)
	case "turn/end":
		var data struct {
			Turn   uint64                `json:"turn"`
			Reason struct{ Kind string } `json:"reason"`
		}
		if json.Unmarshal(event.Data, &data) == nil && data.Turn == state.turn && state.turn != 0 && data.Reason.Kind == "completed" && state.candidate != nil {
			state.results = append(state.results, Completed{Turn: state.turn, CompletedAt: event.Time, Result: *state.candidate})
		}
		state.turn, state.candidate = 0, nil
		clear(state.intents)
	}
	return state, nil
}
func CommittedResults(events []sessionformat.Event) []Completed {
	state := newResultProjection()
	for _, event := range events {
		state, _ = nextResult(state, event)
	}
	return state.results
}

var resultDefinition = sessionformat.ProjectionDefinition[resultProjection]{Key: "proactiveResults", Version: 1, Init: func(sessionformat.Header, uint64) resultProjection { return newResultProjection() }, Apply: nextResult}

func RegisterResultProjection(s *sessionformat.LiveSession) error {
	_, err := sessionformat.RegisterProjection(s, &resultDefinition)
	return err
}
func SessionResult(s *sessionformat.LiveSession, turn uint64) (Completed, bool, error) {
	h, err := sessionformat.RegisterProjection(s, &resultDefinition)
	if err != nil {
		return Completed{}, false, err
	}
	type selected struct {
		value Completed
		found bool
	}
	value, err := sessionformat.ReadProjection(h, sessionformat.DurableCut, func(state resultProjection) selected {
		for i := len(state.results) - 1; i >= 0; i-- {
			result := state.results[i]
			if turn == 0 || result.Turn == turn {
				if result.Result.Notify != nil {
					notify := *result.Result.Notify
					notify.Evidence = make([]Evidence, len(result.Result.Notify.Evidence))
					copy(notify.Evidence, result.Result.Notify.Evidence)
					result.Result.Notify = &notify
				}
				return selected{result, true}
			}
		}
		return selected{}
	})
	return value.value, value.found, err
}
