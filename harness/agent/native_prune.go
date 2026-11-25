package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go-bot/harness/session"
	"unicode/utf16"
)

// These are DeepSeek's tool-result-pruner defaults. The marker and limits are
// intentionally independent of the older, pre-v4 Agent's prune policy.
const (
	nativePruneThreshold = 8192
	nativePruneHead      = 4096
	nativePruneTail      = 1024
	nativePruneMarker    = "\n\n[... tool result middle pruned ...]\n\n"
)

type nativePruneConfig struct {
	threshold int
	head      int
	tail      int
}

func defaultNativePruneConfig() nativePruneConfig {
	return nativePruneConfig{threshold: nativePruneThreshold, head: nativePruneHead, tail: nativePruneTail}
}

func (c nativePruneConfig) validate() error {
	if c.threshold <= 0 || c.head < 0 || c.tail < 0 || c.head+len([]rune(nativePruneMarker))+c.tail > c.threshold {
		return errors.New("invalid native tool-result prune budget")
	}
	return nil
}

// pruneNativeToolResults replaces only current surface nodes. Its paired
// compaction/prune and tool/result events preserve the original result in the
// append-only history and can be replayed without a private overlay.
func (a *NativeLoop) pruneNativeToolResults(ctx context.Context, config nativePruneConfig) (int, error) {
	if err := config.validate(); err != nil {
		return 0, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	surface, err := a.session.ModelSurface()
	if err != nil {
		return 0, err
	}
	if len(surface.MessageSeqs) != len(surface.Messages) {
		return 0, errors.New("native surface lost message sequence mapping")
	}
	openTurn := a.session.TurnBoundary(sessionformat.LiveCut).Open
	pruned := 0
	for index, original := range surface.Messages {
		if err := ctx.Err(); err != nil {
			return pruned, err
		}
		seq := surface.MessageSeqs[index]
		if _, ok := surface.NodeTypes[seq]; !ok {
			return pruned, errors.New("native surface names a missing event")
		}
		if surface.NodeTypes[seq] != "tool/result" {
			continue
		}
		var message map[string]json.RawMessage
		if err := json.Unmarshal(original, &message); err != nil || message == nil {
			return pruned, fmt.Errorf("invalid native tool message at %d: %w", seq, err)
		}
		var blocks []map[string]json.RawMessage
		if err := json.Unmarshal(message["content"], &blocks); err != nil {
			return pruned, err
		}
		replacement, changed, err := pruneNativeContent(blocks, config)
		if err != nil {
			return pruned, err
		}
		if !changed {
			continue
		}
		if !openTurn {
			return pruned, errors.New("native tool-result prune requires an open turn")
		}
		price, err := estimateNativeMessage(original)
		if err != nil {
			return pruned, err
		}
		content, err := json.Marshal(replacement)
		if err != nil {
			return pruned, err
		}
		message["content"] = content
		encodedMessage, err := json.Marshal(message)
		if err != nil {
			return pruned, err
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(surface.ToolResultData[seq], &data); err != nil || data == nil {
			return pruned, fmt.Errorf("invalid native tool result at %d: %w", seq, err)
		}
		data["message"] = encodedMessage
		if _, err := a.append("compaction/prune", map[string]any{
			"shadowedRange": map[string]uint64{"start": seq, "end": seq},
			"shadowedSeqs":  []uint64{seq}, "shadowedTokenCount": price,
		}, nil, nil); err != nil {
			return pruned, err
		}
		op, err := json.Marshal(map[string]any{"op": "replace", "startSeq": seq, "endSeq": seq})
		if err != nil {
			return pruned, err
		}
		if _, err := a.append("tool/result", data, op, []uint64{seq}); err != nil {
			return pruned, err
		}
		pruned++
	}
	if pruned > 0 {
		if err := a.session.Flush(ctx); err != nil {
			return pruned, err
		}
	}
	return pruned, nil
}

func pruneNativeContent(blocks []map[string]json.RawMessage, config nativePruneConfig) ([]map[string]json.RawMessage, bool, error) {
	total := 0
	for _, block := range blocks {
		if string(block["type"]) != `"text"` {
			continue
		}
		var value string
		if err := json.Unmarshal(block["text"], &value); err != nil {
			return nil, false, err
		}
		total += len([]rune(value))
	}
	if total <= config.threshold {
		return nil, false, nil
	}
	removedStart, removedEnd := config.head, total-config.tail
	result := make([]map[string]json.RawMessage, 0, len(blocks))
	consumed, marked := 0, false
	for _, block := range blocks {
		if string(block["type"]) != `"text"` {
			result = append(result, block)
			continue
		}
		var value string
		if err := json.Unmarshal(block["text"], &value); err != nil {
			return nil, false, err
		}
		points := []rune(value)
		start, end := consumed, consumed+len(points)
		headEnd := min(len(points), max(0, removedStart-start))
		tailStart := min(len(points), max(0, removedEnd-start))
		marker := ""
		if start < removedEnd && end > removedStart && !marked {
			marker, marked = nativePruneMarker, true
		}
		text := string(points[:headEnd]) + marker + string(points[tailStart:])
		if text != "" {
			copyBlock := make(map[string]json.RawMessage, len(block))
			for key, value := range block {
				copyBlock[key] = value
			}
			encoded, err := json.Marshal(text)
			if err != nil {
				return nil, false, err
			}
			copyBlock["text"] = encoded
			result = append(result, copyBlock)
		}
		consumed = end
	}
	if !marked {
		return nil, false, errors.New("native prune found no removed text span")
	}
	after := 0
	for _, block := range result {
		if string(block["type"]) == `"text"` {
			var value string
			if err := json.Unmarshal(block["text"], &value); err != nil {
				return nil, false, err
			}
			after += len([]rune(value))
		}
	}
	if after > config.threshold || after >= total {
		return nil, false, errors.New("native prune replacement did not reduce text within budget")
	}
	return result, true, nil
}

// DeepSeek's fixed token-meter heuristic prices one model-visible message.
// JS string length is UTF-16 code units, whereas pruning uses code points.
func estimateNativeMessage(raw json.RawMessage) (int, error) {
	var message struct {
		Role    string            `json:"role"`
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		return 0, err
	}
	if message.Role == "system" {
		if len(message.Content) == 0 {
			return 0, nil
		}
		characters := 0
		for _, rawBlock := range message.Content {
			var block struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(rawBlock, &block); err != nil {
				return 0, err
			}
			if block.Type == "text" {
				characters += len(utf16.Encode([]rune(block.Text)))
			} else {
				characters += len(rawBlock)
			}
		}
		return (characters+3)/4 + 4, nil
	}
	price := 4
	for _, rawBlock := range message.Content {
		var block struct {
			Type      string `json:"type"`
			Text      string `json:"text"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			return 0, err
		}
		switch block.Type {
		case "text", "reasoning":
			price += (len(utf16.Encode([]rune(block.Text)))+3)/4 + 4
		case "tool-call":
			price += (len(utf16.Encode([]rune(block.Name)))+3)/4 + (len(utf16.Encode([]rune(block.Arguments)))+3)/4 + 4
		default:
			var canonical map[string]json.RawMessage
			if err := json.Unmarshal(rawBlock, &canonical); err != nil {
				return 0, err
			}
			if block.Type == "image" {
				delete(canonical, "offloaded")
			}
			encoded, err := nativePriceJSON(canonical)
			if err != nil {
				return 0, err
			}
			price += (len(utf16.Encode([]rune(string(encoded))))+3)/4 + 4
		}
	}
	return price, nil
}
