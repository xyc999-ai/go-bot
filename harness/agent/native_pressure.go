package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"unicode/utf16"

	"go-bot/harness/session"
)

// nativeSurfaceTokens uses the same fixed per-message heuristic as the v4
// tool-result pruner. Capacity is unavailable when the route has no configured
// context window; provider-confirmed overflow recovery still applies then.
func nativeSurfaceTokens(surface sessionformat.Surface) (int, error) {
	prices, err := nativeSurfacePrices(surface, nil)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, price := range prices {
		total += price.heuristic
	}
	return total, nil
}

type nativeSurfacePrice struct {
	heuristic int
	request   int
}

// The durable file block is only a reference. Request assembly sends the
// current execution world's readable-path handle instead, so price that text
// for pressure and retention while keeping the fixed price for shadow claims.
func nativeSurfacePrices(surface sessionformat.Surface, resolveFile NativeFilePathResolver) ([]nativeSurfacePrice, error) {
	if len(surface.Messages) != len(surface.MessageSeqs) {
		return nil, errors.New("native surface lost message sequence mapping")
	}
	prices := make([]nativeSurfacePrice, len(surface.Nodes))
	positions := make(map[uint64]int, len(surface.Nodes))
	for index, seq := range surface.Nodes {
		positions[seq] = index
	}
	for index, message := range surface.Messages {
		position, found := positions[surface.MessageSeqs[index]]
		if !found {
			return nil, errors.New("native surface message is outside the current surface")
		}
		heuristic, err := estimateNativeMessage(message)
		if err != nil {
			return nil, err
		}
		delta, err := nativeFileProjectionDelta(message, resolveFile)
		if err != nil {
			return nil, err
		}
		prices[position] = nativeSurfacePrice{heuristic: heuristic, request: heuristic + delta}
	}
	return prices, nil
}

func nativeFileProjectionDelta(message json.RawMessage, resolveFile NativeFilePathResolver) (int, error) {
	var value struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(message, &value); err != nil {
		return 0, err
	}
	return nativeFileBlocksDelta(value.Content, resolveFile)
}

func nativeFileBlocksDelta(blocks []json.RawMessage, resolveFile NativeFilePathResolver) (int, error) {
	delta := 0
	for _, raw := range blocks {
		var block struct {
			Type       string            `json:"type"`
			Content    []json.RawMessage `json:"content"`
			Attachment struct {
				ID    string `json:"attachmentId"`
				Name  string `json:"name"`
				Bytes int64  `json:"bytes"`
			} `json:"attachment"`
		}
		if err := json.Unmarshal(raw, &block); err != nil {
			return 0, err
		}
		switch block.Type {
		case "tool-result":
			nested, err := nativeFileBlocksDelta(block.Content, resolveFile)
			if err != nil {
				return 0, err
			}
			delta += nested
		case "file":
			ref := NativeFileAttachmentRef{ID: block.Attachment.ID, Name: block.Attachment.Name, Bytes: block.Attachment.Bytes}
			if ref.ID == "" || ref.Name == "" || ref.Bytes < 0 {
				return 0, errors.New("native file block lacks a valid attachment")
			}
			path := ""
			if resolveFile != nil {
				path = resolveFile(ref)
			}
			handle := nativeFileHandleText(ref, path)
			var canonical map[string]json.RawMessage
			if err := json.Unmarshal(raw, &canonical); err != nil {
				return 0, err
			}
			encoded, err := nativePriceJSON(canonical)
			if err != nil {
				return 0, err
			}
			structural := (len(utf16.Encode([]rune(string(encoded))))+3)/4 + 4
			text := (len(utf16.Encode([]rune(handle)))+3)/4 + 4
			delta += text - structural
		}
	}
	return delta, nil
}

// The tool schemas are part of every routed request even though they are not
// surface nodes. DeepSeek's token meter adds their canonical request-header
// JSON price once, outside the per-message prices.
func nativeRequestTokens(surface sessionformat.Surface, tools []ToolSpec, resolveFile NativeFilePathResolver) (int, error) {
	prices, err := nativeSurfacePrices(surface, resolveFile)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, price := range prices {
		total += price.request
	}
	toolTokens, err := nativeToolTokens(tools)
	return total + toolTokens, err
}

func nativeToolTokens(tools []ToolSpec) (int, error) {
	if len(tools) == 0 {
		return 0, nil
	}
	canonical, err := nativeCanonicalTools(tools)
	if err != nil {
		return 0, err
	}
	encoded, err := nativePriceJSON(canonical)
	if err != nil {
		return 0, err
	}
	return (len(utf16.Encode([]rune(string(encoded))))+3)/4 + 4, nil
}

// Token-meter structural prices follow JSON.stringify, which leaves HTML
// characters literal. encoding/json's default HTML escaping changes the
// UTF-16 length and would overprice otherwise identical model-visible JSON.
func nativePriceJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	encoded := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	// encoding/json escapes the two JavaScript line-separator runes even when
	// HTML escaping is disabled. JSON.stringify leaves them literal. Walk JSON
	// string escapes rather than ReplaceAll: a literal "\\u2028" must stay a
	// literal backslash sequence, not become a separator rune.
	if !bytes.Contains(encoded, []byte(`\u202`)) {
		return encoded, nil
	}
	output := make([]byte, 0, len(encoded))
	inString := false
	for index := 0; index < len(encoded); {
		if encoded[index] == '"' {
			inString = !inString
			output = append(output, encoded[index])
			index++
			continue
		}
		if inString && encoded[index] == '\\' && index+1 < len(encoded) {
			if index+5 < len(encoded) && encoded[index+1] == 'u' &&
				bytes.Equal(encoded[index+2:index+5], []byte("202")) &&
				(encoded[index+5] == '8' || encoded[index+5] == '9') {
				output = append(output, 0xe2, 0x80, 0xa8+encoded[index+5]-'8')
				index += 6
				continue
			}
			output = append(output, encoded[index:index+2]...)
			index += 2
			continue
		}
		output = append(output, encoded[index])
		index++
	}
	return output, nil
}

// DeepSeek reuses a provider sample only for the latest successful call with
// an identical request envelope, and only when that sample covers the full
// estimated anchored request. Reprice both surfaces with the current file
// resolver so changed saved-copy availability produces a signed delta.
func (a *NativeLoop) nativeMeasuredRequestTokens(surface sessionformat.Surface) (int, error) {
	estimated, err := nativeRequestTokens(surface, a.toolSpecs, a.filePath)
	if err != nil {
		return 0, err
	}
	anchor, err := a.latestNativePressureAnchor()
	if err != nil {
		return 0, err
	}
	if anchor == nil || anchor.header == nil {
		return estimated, nil
	}
	current, err := a.nativeRequestHeader()
	if err != nil {
		return 0, err
	}
	if !reflect.DeepEqual(anchor.header, current) {
		return estimated, nil
	}
	usage := anchor.usage
	providerTotal := int64(0)
	for _, amount := range []int64{usage.InputTokens, usage.CacheReadTokens, usage.CacheWriteTokens, usage.OutputTokens} {
		if amount < 0 || providerTotal > math.MaxInt64-amount {
			return 0, ErrInvalidProviderUsage
		}
		providerTotal += amount
	}
	beforeTokens, err := nativeRequestTokens(anchor.before, nil, a.filePath)
	if err != nil {
		return 0, err
	}
	var assistantMessage struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(anchor.message, &assistantMessage); err != nil {
		return 0, err
	}
	assistantTokens := 0
	if len(assistantMessage.Content) > 0 {
		assistantTokens, err = estimateNativeMessage(anchor.message)
		if err != nil {
			return 0, err
		}
	}
	toolTokens, err := nativeToolTokens(a.toolSpecs)
	if err != nil {
		return 0, err
	}
	anchoredSurface := beforeTokens + assistantTokens
	if providerTotal < int64(toolTokens+anchoredSurface) {
		return estimated, nil
	}
	measured := providerTotal + int64(estimated-toolTokens-anchoredSurface)
	if measured < 0 {
		return 0, nil
	}
	if measured > int64(math.MaxInt) {
		return 0, errors.New("native token pressure exceeds integer capacity")
	}
	return int(measured), nil
}

func (a *NativeLoop) nativePressureThreshold() (threshold, retain int) {
	if a.contextWindowTokens == 0 {
		return 0, 0
	}
	messageBudget := a.contextWindowTokens - a.reservedOutputTokens
	return min(a.contextWindowTokens*8/10, messageBudget-a.contextHeadroomTokens), messageBudget * 16 / 100
}

// maybeCompactNativePressure runs at the step boundary after the user and
// system messages have been admitted but before the model request is logged.
// It mirrors the upstream pressure path: measure, prune only after qualifying,
// then summarize up to two times while pressure remains above the threshold.
func (a *NativeLoop) maybeCompactNativePressure(ctx context.Context, turn uint64) error {
	threshold, retain := a.nativePressureThreshold()
	if threshold == 0 {
		return nil
	}
	measure := func() (int, error) {
		surface, err := a.session.ModelSurface()
		if err != nil {
			return 0, err
		}
		return a.nativeMeasuredRequestTokens(surface)
	}
	tokens, err := measure()
	if err != nil || tokens < threshold {
		return err
	}
	if _, err := a.pruneNativeToolResults(ctx, defaultNativePruneConfig()); err != nil {
		return err
	}
	tokens, err = measure()
	if err != nil || tokens < threshold {
		return err
	}
	for attempt := 0; attempt < 2; attempt++ {
		compacted, err := a.compactNative(ctx, turn, retain)
		var summaryFailure *nativeSummaryFailure
		if errors.As(err, &summaryFailure) && ctx.Err() == nil {
			// The failed bracket is closed and durable; run the ordinary request
			// against the unchanged surface, as the upstream auto path does.
			return nil
		}
		if err != nil || !compacted {
			return err
		}
		// The replacement surface may have shadowed the baseline admitted at
		// this very step. Reconcile before measuring or sending a model request;
		// otherwise one pressure-triggered call silently loses live guidance.
		if err := a.ensureInstructions(ctx); err != nil {
			return err
		}
		if err := a.session.Flush(ctx); err != nil {
			return err
		}
		tokens, err = measure()
		if err != nil || tokens < threshold {
			return err
		}
	}
	return fmt.Errorf("native compaction still above threshold after 2 attempts (%d estimated tokens >= %d)", tokens, threshold)
}
