package sessionformat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// Surface is the model-visible order obtained by replaying v4 surface events.
// Nodes are log sequences, not message indexes: an empty system or assistant
// message retains its node even though it contributes no model input.
type Surface struct {
	// Metadata covers current nodes only, including nodes with empty content.
	ToolResultData map[uint64]json.RawMessage `json:"-"`
	NodeTypes      map[uint64]string          `json:"-"`
	NodeMessages   map[uint64]json.RawMessage `json:"-"`
	Nodes          []uint64
	Messages       []json.RawMessage
	MessageSeqs    []uint64
	Replacements   []SurfaceReplacement
}

type SurfaceReplacement struct {
	Seq          uint64
	StartSeq     uint64
	EndSeq       uint64
	ShadowedSeqs []uint64
}

// SurfaceState accepts one event at a time, as DeepSeek's live Session does.
// It keeps the ordered nodes and image projection, avoiding a full log fold on
// every append. A rejected event leaves the state unchanged.
type SurfaceState struct {
	events       []Event
	nodes        []uint64
	replacements []SurfaceReplacement
	projected    map[uint64]json.RawMessage
}

func NewSurfaceState(seed []Event) (*SurfaceState, error) {
	state := &SurfaceState{projected: make(map[uint64]json.RawMessage)}
	for index, event := range seed {
		if err := state.Append(event); err != nil {
			return nil, fmt.Errorf("surface event %d: %w", index, err)
		}
	}
	return state, nil
}

func (s *SurfaceState) Append(event Event) error {
	if s == nil {
		return errors.New("nil Session surface")
	}
	if err := event.ValidateEnvelope(uint64(len(s.events))); err != nil {
		return err
	}
	if s.projected == nil {
		s.projected = make(map[uint64]json.RawMessage)
	}
	if event.Type == "image/offload" {
		if err := applyImageOffload(event, s.events, s.nodes, s.projected); err != nil {
			return err
		}
	} else if !event.Ignorable || surfaceEligible(event.Type) {
		if len(event.SurfaceOp) != 0 {
			if err := validateMessage(event); err != nil {
				return err
			}
			if bytes.Equal(bytes.TrimSpace(event.SurfaceOp), []byte(`"append"`)) {
				s.nodes = append(s.nodes, event.Seq)
			} else {
				var operation struct {
					Op       string `json:"op"`
					StartSeq uint64 `json:"startSeq"`
					EndSeq   uint64 `json:"endSeq"`
				}
				if err := json.Unmarshal(event.SurfaceOp, &operation); err != nil || operation.Op != "replace" {
					return errors.New("invalid surface replacement")
				}
				start, end := nodeIndex(s.nodes, operation.StartSeq), nodeIndex(s.nodes, operation.EndSeq)
				if start < 0 || end < 0 || start > end {
					return errors.New("replacement names nodes outside the current surface")
				}
				shadowed := append([]uint64(nil), s.nodes[start:end+1]...)
				sources, err := expandSourceSeqs(event.SourceEventSeqs)
				if err != nil {
					return err
				}
				for _, seq := range shadowed {
					if _, present := sources[seq]; !present {
						return fmt.Errorf("replacement omits shadowed source %d", seq)
					}
				}
				if start == 0 && s.events[s.nodes[0]].Type == "system/message" && (event.Type != "system/message" || len(shadowed) != 1) {
					return errors.New("system prompt head may only be replaced by one system/message")
				}
				if event.Type == "tool/result" && (len(shadowed) != 1 || s.events[shadowed[0]].Type != "tool/result" || !sameToolResultApartFromContent(s.events[shadowed[0]].Data, event.Data)) {
					return errors.New("tool/result replacement may change only one current result's content")
				}
				s.replacements = append(s.replacements, SurfaceReplacement{Seq: event.Seq, StartSeq: operation.StartSeq, EndSeq: operation.EndSeq, ShadowedSeqs: shadowed})
				next := make([]uint64, 0, len(s.nodes)-(end-start))
				next = append(next, s.nodes[:start]...)
				next = append(next, event.Seq)
				next = append(next, s.nodes[end+1:]...)
				s.nodes = next
			}
		}
	}
	s.events = append(s.events, cloneV4Event(event))
	return nil
}

func (s *SurfaceState) Snapshot() (Surface, error) { return s.snapshot(true) }

// ModelSnapshot contains the current surface, without the growing replacement
// audit trail. Inspection and replay still expose that trail through Snapshot.
func (s *SurfaceState) ModelSnapshot() (Surface, error) { return s.snapshot(false) }
func (s *SurfaceState) snapshot(includeReplacements bool) (Surface, error) {
	if s == nil {
		return Surface{}, errors.New("nil Session surface")
	}
	result := Surface{ToolResultData: make(map[uint64]json.RawMessage), Nodes: append([]uint64(nil), s.nodes...), NodeTypes: make(map[uint64]string, len(s.nodes)), NodeMessages: make(map[uint64]json.RawMessage, len(s.nodes))}
	if includeReplacements {
		for _, replacement := range s.replacements {
			replacement.ShadowedSeqs = append([]uint64(nil), replacement.ShadowedSeqs...)
			result.Replacements = append(result.Replacements, replacement)
		}
	}
	for _, seq := range s.nodes {
		message, include, err := eventMessage(s.events[seq])
		if err != nil {
			return Surface{}, err
		}
		if replacement, found := s.projected[seq]; found {
			message = append(json.RawMessage(nil), replacement...)
		}
		if s.events[seq].Type == "tool/result" {
			result.ToolResultData[seq] = append(json.RawMessage(nil), s.events[seq].Data...)
		}
		result.NodeTypes[seq] = s.events[seq].Type
		result.NodeMessages[seq] = message
		if include {
			result.Messages = append(result.Messages, message)
			result.MessageSeqs = append(result.MessageSeqs, seq)
		}
	}
	return result, nil
}

func cloneV4Event(event Event) Event {
	event.Data = append(json.RawMessage(nil), event.Data...)
	event.SurfaceOp = append(json.RawMessage(nil), event.SurfaceOp...)
	event.SourceEventSeqs = append(json.RawMessage(nil), event.SourceEventSeqs...)
	return event
}

// FoldSurface replays native v4 append/replace and image/offload decisions.
func FoldSurface(events []Event) (Surface, error) {
	state, err := NewSurfaceState(events)
	if err != nil {
		return Surface{}, err
	}
	return state.Snapshot()
}

func applyImageOffload(event Event, events []Event, nodes []uint64, projected map[uint64]json.RawMessage) error {
	var data map[string]json.RawMessage
	if json.Unmarshal(event.Data, &data) != nil || len(data) != 1 {
		return errors.New("image/offload requires only a nonempty targets array")
	}
	var targets []json.RawMessage
	if json.Unmarshal(data["targets"], &targets) != nil || len(targets) == 0 {
		return errors.New("image/offload requires only a nonempty targets array")
	}
	updates := make(map[uint64]json.RawMessage, len(targets))
	for _, raw := range targets {
		var target map[string]json.RawMessage
		if json.Unmarshal(raw, &target) != nil || len(target) != 2 {
			return errors.New("image/offload target requires seq and imageIndexes")
		}
		seq, err := safeCount(target["seq"])
		if err != nil || seq >= event.Seq || nodeIndex(nodes, seq) < 0 {
			return errors.New("image/offload target must name a current surface node")
		}
		if _, duplicate := updates[seq]; duplicate {
			return errors.New("image/offload has duplicate target seq")
		}
		if events[seq].Type != "user/message" && events[seq].Type != "tool/result" {
			return errors.New("image/offload target must be user/message or tool/result")
		}
		var rawIndexes []json.RawMessage
		if json.Unmarshal(target["imageIndexes"], &rawIndexes) != nil || len(rawIndexes) == 0 {
			return errors.New("image/offload target requires nonempty imageIndexes")
		}
		indexes := make([]uint64, 0, len(rawIndexes))
		for _, rawIndex := range rawIndexes {
			imageIndex, err := safeCount(rawIndex)
			if err != nil || len(indexes) > 0 && imageIndex <= indexes[len(indexes)-1] {
				return errors.New("image/offload imageIndexes must be strictly increasing")
			}
			indexes = append(indexes, imageIndex)
		}
		message := projected[seq]
		if len(message) == 0 {
			message, _, err = eventMessage(events[seq])
			if err != nil {
				return err
			}
		}
		updated, err := offloadMessageImages(message, indexes)
		if err != nil {
			return err
		}
		updates[seq] = updated
	}
	for seq, message := range updates {
		projected[seq] = message
	}
	return nil
}

func offloadMessageImages(raw json.RawMessage, indexes []uint64) (json.RawMessage, error) {
	var message map[string]json.RawMessage
	if json.Unmarshal(raw, &message) != nil || message == nil {
		return nil, errors.New("image/offload target has no message")
	}
	var blocks []json.RawMessage
	if json.Unmarshal(message["content"], &blocks) != nil || blocks == nil {
		return nil, errors.New("image/offload target has no content array")
	}
	var imageIndex uint64
	selected := 0
	for index, rawBlock := range blocks {
		var block map[string]json.RawMessage
		if json.Unmarshal(rawBlock, &block) != nil || block == nil || !bytes.Equal(block["type"], []byte(`"image"`)) {
			continue
		}
		if selected < len(indexes) && indexes[selected] == imageIndex {
			if bytes.Equal(block["offloaded"], []byte("true")) {
				return nil, fmt.Errorf("image/offload image index %d is already offloaded", imageIndex)
			}
			block["offloaded"] = json.RawMessage("true")
			updated, err := json.Marshal(block)
			if err != nil {
				return nil, err
			}
			blocks[index] = updated
			selected++
		}
		imageIndex++
	}
	if selected != len(indexes) {
		return nil, fmt.Errorf("image/offload image index %d does not exist", indexes[selected])
	}
	content, err := json.Marshal(blocks)
	if err != nil {
		return nil, err
	}
	message["content"] = content
	return json.Marshal(message)
}

func surfaceEligible(eventType string) bool {
	switch eventType {
	case "system/message", "developer/message", "user/message", "assistant/message", "tool/result":
		return true
	default:
		return false
	}
}

func nodeIndex(nodes []uint64, seq uint64) int {
	for index, node := range nodes {
		if node == seq {
			return index
		}
	}
	return -1
}

func expandSourceSeqs(raw json.RawMessage) (map[uint64]struct{}, error) {
	var entries []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &entries) != nil || len(entries) == 0 {
		return nil, errors.New("surface replacement requires sourceEventSeqs")
	}
	sources := make(map[uint64]struct{})
	for _, entry := range entries {
		start, end, _, err := decodeSourceEntry(entry)
		if err != nil || end < start {
			return nil, errors.New("invalid sourceEventSeqs range")
		}
		for seq := start; seq <= end; seq++ {
			sources[seq] = struct{}{}
		}
	}
	return sources, nil
}

func eventMessage(event Event) (json.RawMessage, bool, error) {
	if event.Type == "user/message" {
		return append(json.RawMessage(nil), event.Data...), true, nil
	}
	var data struct {
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(event.Data, &data); err != nil || len(data.Message) == 0 {
		return nil, false, errors.New("surface event has no message")
	}
	if event.Type == "tool/result" {
		return append(json.RawMessage(nil), data.Message...), true, nil
	}
	var message struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data.Message, &message); err != nil {
		return nil, false, err
	}
	if len(message.Content) == 0 {
		return append(json.RawMessage(nil), data.Message...), false, nil
	}
	return append(json.RawMessage(nil), data.Message...), true, nil
}

func validateMessage(event Event) error {
	message, _, err := eventMessage(event)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(message, &fields); err != nil || fields == nil {
		return errors.New("surface message must be an object")
	}
	var id, role string
	if json.Unmarshal(fields["id"], &id) != nil || id == "" || json.Unmarshal(fields["role"], &role) != nil {
		return errors.New("surface message requires an id and role")
	}
	want := map[string]string{"system/message": "system", "developer/message": "developer", "user/message": "user", "assistant/message": "assistant", "tool/result": "tool"}[event.Type]
	if role != want {
		return fmt.Errorf("%s requires %s role", event.Type, want)
	}
	var source map[string]json.RawMessage
	var kind string
	if json.Unmarshal(fields["source"], &source) != nil || source == nil || json.Unmarshal(source["kind"], &kind) != nil || kind == "" || kind == "plugin" {
		return errors.New("surface message requires a producer-owned source")
	}
	var content []json.RawMessage
	if json.Unmarshal(fields["content"], &content) != nil || content == nil {
		return errors.New("surface message requires array content")
	}
	if event.Type == "assistant/message" {
		var data struct {
			Stream []json.RawMessage `json:"stream"`
		}
		if json.Unmarshal(event.Data, &data) != nil || data.Stream == nil {
			return errors.New("assistant/message requires its stream")
		}
	}
	if event.Type == "tool/result" {
		var callID, sourceCallID string
		if json.Unmarshal(fields["toolCallId"], &callID) != nil || callID == "" || json.Unmarshal(source["callId"], &sourceCallID) != nil || sourceCallID != callID || kind != "tool" {
			return errors.New("tool/result requires matching toolCallId and tool source")
		}
		for _, block := range content {
			var value map[string]json.RawMessage
			if json.Unmarshal(block, &value) == nil && value != nil && bytes.Equal(value["type"], []byte(`"tool-result"`)) {
				return errors.New("tool/result content cannot contain a retired wrapper")
			}
		}
	}
	return nil
}

func sameToolResultApartFromContent(before, after json.RawMessage) bool {
	var original, replacement map[string]any
	if json.Unmarshal(before, &original) != nil || json.Unmarshal(after, &replacement) != nil {
		return false
	}
	oldMessage, oldOK := original["message"].(map[string]any)
	newMessage, newOK := replacement["message"].(map[string]any)
	if !oldOK || !newOK {
		return false
	}
	oldMessage["content"], newMessage["content"] = nil, nil
	return reflect.DeepEqual(original, replacement)
}
