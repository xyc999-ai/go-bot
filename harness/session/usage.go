package sessionformat

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// UsageSnapshot is execution telemetry projected from canonical v4
// events. It is neither a provider call ledger nor a Credits balance: an
// assistant/attempt can lack a provider usage report, and a step can end
// before any request reaches the provider.
type UsageSnapshot struct {
	StepsStarted      uint64 `json:"stepsStarted"`
	AssistantMessages uint64 `json:"assistantMessages"`
	AssistantAttempts uint64 `json:"assistantAttempts"`
	SummarySucceeded  uint64 `json:"summarySucceeded"`
	SummaryFailed     uint64 `json:"summaryFailed"`
	ReportedSamples   uint64 `json:"reportedSamples"`
	UnmeteredOutcomes uint64 `json:"unmeteredOutcomes"`
	InputTokens       int64  `json:"inputTokens"`
	OutputTokens      int64  `json:"outputTokens"`
	TotalTokens       int64  `json:"totalTokens"`
	CacheReadTokens   int64  `json:"cacheReadTokens"`
	CacheWriteTokens  int64  `json:"cacheWriteTokens"`
}

type v4Usage struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
}

// ProjectUsage folds only durable Session events. The canonical v4 artifact
// remains the fact source; no separate meter is persisted or consulted when
// a worker resumes.
func nextUsage(result UsageSnapshot, event Event) (UsageSnapshot, error) {
	var data struct {
		Usage  json.RawMessage `json:"usage"`
		Error  json.RawMessage `json:"error"`
		Stream []struct {
			Type  string `json:"type"`
			Chunk struct {
				Type  string          `json:"type"`
				Usage json.RawMessage `json:"usage"`
			} `json:"chunk"`
		} `json:"stream"`
	}
	switch event.Type {
	case "step/start":
		result.StepsStarted++
		return result, nil
	case "assistant/message":
		if isInitialWelcomeMessage(event) {
			return result, nil
		}
		result.AssistantMessages++
	case "assistant/attempt":
		result.AssistantAttempts++
	case "compaction/summary":
		result.SummarySucceeded++
	case "compaction/end":
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return UsageSnapshot{}, fmt.Errorf("decode v4 compaction end %d: %w", event.Seq, err)
		}
		if len(data.Error) > 0 && string(data.Error) != "null" {
			result.SummaryFailed++
		}
		return result, nil
	default:
		return result, nil
	}
	if err := json.Unmarshal(event.Data, &data); err != nil {
		return UsageSnapshot{}, fmt.Errorf("decode v4 usage event %d: %w", event.Seq, err)
	}
	usage := data.Usage
	if event.Type == "assistant/attempt" {
		for _, item := range data.Stream {
			if item.Type == "chunk" && item.Chunk.Type == "usage" {
				usage = item.Chunk.Usage
			}
		}
	}
	if len(usage) == 0 || string(usage) == "null" {
		result.UnmeteredOutcomes++
		return result, nil
	}
	var sample v4Usage
	if err := json.Unmarshal(usage, &sample); err != nil {
		return UsageSnapshot{}, fmt.Errorf("decode v4 usage sample %d: %w", event.Seq, err)
	}
	if err := result.addUsage(sample); err != nil {
		return UsageSnapshot{}, fmt.Errorf("v4 usage sample %d: %w", event.Seq, err)
	}
	return result, nil
}
func ProjectUsage(events []Event) (UsageSnapshot, error) {
	var result UsageSnapshot
	for _, event := range events {
		var err error
		result, err = nextUsage(result, event)
		if err != nil {
			return UsageSnapshot{}, err
		}
	}
	return result, nil
}

var usageDefinition = ProjectionDefinition[UsageSnapshot]{Key: "usage", Version: 1, Init: func(Header, uint64) UsageSnapshot { return UsageSnapshot{} }, Apply: nextUsage}

func (s *LiveSession) Usage() (UsageSnapshot, error) {
	return ReadProjection(ProjectionHandle[UsageSnapshot]{s, &usageDefinition}, DurableCut, func(v UsageSnapshot) UsageSnapshot { return v })
}

func (s *UsageSnapshot) addUsage(usage v4Usage) error {
	values := []*int64{&s.InputTokens, &s.OutputTokens, &s.CacheReadTokens, &s.CacheWriteTokens}
	deltas := []int64{usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens}
	var total int64
	for index, delta := range deltas {
		if delta < 0 || *values[index] > math.MaxInt64-delta || total > math.MaxInt64-delta {
			return errors.New("negative or overflowing token count")
		}
		total += delta
	}
	if s.TotalTokens > math.MaxInt64-total {
		return errors.New("overflowing total token count")
	}
	for index, delta := range deltas {
		*values[index] += delta
	}
	s.TotalTokens += total
	s.ReportedSamples++
	return nil
}
