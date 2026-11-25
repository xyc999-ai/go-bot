package interaction

import (
	"encoding/json"
	"slices"

	"go-bot/harness/session"
)

// FoldTimedQuestions derives answerable questions solely from the v4 log.
// The tool schema in each request/header distinguishes timed calls from older
// blocking calls, as in the upstream userQuestions projection.
type questionProjectionState struct {
	active map[string]Pending
	order  []string
	timed  bool
}

func newQuestionProjection() questionProjectionState {
	return questionProjectionState{active: make(map[string]Pending), order: []string{}}
}
func nextTimedQuestion(state questionProjectionState, event sessionformat.Event) (questionProjectionState, error) {
	switch event.Type {
	case "request/header":
		var row struct {
			Header struct {
				Tools []struct {
					Name       string                     `json:"name"`
					Parameters map[string]json.RawMessage `json:"parameters"`
				} `json:"tools"`
			} `json:"header"`
		}
		if json.Unmarshal(event.Data, &row) != nil {
			state.timed = false
			return state, nil
		}
		state.timed = false
		for _, tool := range row.Header.Tools {
			if tool.Name != "ask_user_question" {
				continue
			}
			var properties map[string]json.RawMessage
			if json.Unmarshal(tool.Parameters["properties"], &properties) == nil {
				_, state.timed = properties["timeout"]
			}
		}
	case "tool/call":
		if !state.timed {
			return state, nil
		}
		var row struct {
			Turn      uint64 `json:"turn"`
			Step      uint64 `json:"step"`
			CallID    string `json:"callId"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if json.Unmarshal(event.Data, &row) != nil || row.Name != "ask_user_question" || row.CallID == "" {
			return state, nil
		}
		var args struct {
			Questions []struct {
				ID          string   `json:"id"`
				Question    string   `json:"question"`
				Header      string   `json:"header"`
				Options     []Option `json:"options"`
				MultiSelect bool     `json:"multi_select"`
			} `json:"questions"`
		}
		if json.Unmarshal([]byte(row.Arguments), &args) != nil || len(args.Questions) == 0 {
			return state, nil
		}
		questions := make([]Question, 0, len(args.Questions))
		for _, item := range args.Questions {
			questions = append(questions, Question{ID: item.ID, Question: item.Question, Header: item.Header,
				Options: item.Options, MultiSelect: item.MultiSelect})
		}
		if ValidateQuestions(questions) != nil {
			return state, nil
		}
		for index, id := range state.order {
			if id == row.CallID {
				state.order = append(state.order[:index], state.order[index+1:]...)
				break
			}
		}
		state.order = append(state.order, row.CallID)
		state.active[row.CallID] = Pending{CallID: row.CallID, Questions: questions, Turn: row.Turn, Step: row.Step, State: "open"}
	case "tool/result":
		var row struct {
			Message struct {
				ToolCallID string `json:"toolCallId"`
				IsError    bool   `json:"isError"`
				Content    []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(event.Data, &row) != nil {
			return state, nil
		}
		pending, ok := state.active[row.Message.ToolCallID]
		if !ok {
			return state, nil
		}
		if row.Error.Code == "TOOL_OUTCOME_UNKNOWN" {
			pending.State = "continued"
			state.active[pending.CallID] = pending
			return state, nil
		}
		isPending := false
		if !row.Message.IsError && len(row.Message.Content) > 0 {
			var payload struct {
				Pending bool `json:"pending"`
			}
			isPending = json.Unmarshal([]byte(row.Message.Content[0].Text), &payload) == nil && payload.Pending
		}
		if isPending {
			pending.State = "continued"
			state.active[pending.CallID] = pending
		} else {
			delete(state.active, pending.CallID)
			state.order = slices.DeleteFunc(state.order, func(id string) bool { return id == pending.CallID })
		}
	case "user/message":
		var row struct {
			Source struct {
				Kind   string `json:"kind"`
				CallID string `json:"callId"`
			} `json:"source"`
		}
		if json.Unmarshal(event.Data, &row) == nil && row.Source.Kind == "user-question-reply" {
			delete(state.active, row.Source.CallID)
			state.order = slices.DeleteFunc(state.order, func(id string) bool { return id == row.Source.CallID })
		}
	}
	return state, nil
}
func questionView(state questionProjectionState) []Pending {
	result := make([]Pending, 0, len(state.active))
	for _, id := range state.order {
		if pending, ok := state.active[id]; ok {
			pending.Questions = append([]Question(nil), pending.Questions...)
			for i := range pending.Questions {
				pending.Questions[i].Options = append([]Option(nil), pending.Questions[i].Options...)
			}
			result = append(result, pending)
		}
	}
	return result
}
func FoldTimedQuestions(events []sessionformat.Event) []Pending {
	state := newQuestionProjection()
	for _, event := range events {
		state, _ = nextTimedQuestion(state, event)
	}
	return questionView(state)
}

var questionDefinition = sessionformat.ProjectionDefinition[questionProjectionState]{Key: "userQuestions", Version: 1, Init: func(sessionformat.Header, uint64) questionProjectionState { return newQuestionProjection() }, Apply: nextTimedQuestion, View: func(state questionProjectionState) (json.RawMessage, error) { return json.Marshal(questionView(state)) }}

func RegisterQuestionProjection(s *sessionformat.LiveSession) (sessionformat.ProjectionHandle[questionProjectionState], error) {
	return sessionformat.RegisterProjection(s, &questionDefinition)
}
func SessionQuestions(s *sessionformat.LiveSession) ([]Pending, error) {
	handle, err := RegisterQuestionProjection(s)
	if err != nil {
		return nil, err
	}
	return sessionformat.ReadProjection(handle, sessionformat.DurableCut, questionView)
}
