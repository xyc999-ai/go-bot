package agent

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/google/uuid"

	"go-bot/internal/modeldata"
	"go-bot/thirdparty/llm"
)

// DeepSeek's default provider policy: five bounded retries for transient
// model-request failures, with 500 ms exponential backoff, a 10 s cap and
// symmetric 10% jitter. It is intentionally owned by the Agent loop so every
// upstream attempt remains visible in the Session log.
type nativeRetryPolicy struct {
	maxRetries   int
	initialDelay time.Duration
	maxDelay     time.Duration
	jitterRatio  float64
	random       func() float64
	wait         func(context.Context, time.Duration) error
}

var nativeRetryableCodes = []string{"EMPTY_RESPONSE", "RATE_LIMIT", "SERVER", "TIMEOUT", "TRANSPORT"}

func defaultNativeRetryPolicy() nativeRetryPolicy {
	return nativeRetryPolicy{
		maxRetries: 5, initialDelay: 500 * time.Millisecond, maxDelay: 10 * time.Second,
		jitterRatio: 0.1, random: rand.Float64, wait: waitModelRetry,
	}
}

func (p nativeRetryPolicy) key() string {
	codes := append([]string(nil), nativeRetryableCodes...)
	sort.Strings(codes)
	encoded, _ := json.Marshal([]any{"normal", p.maxRetries, codes, p.initialDelay.Milliseconds(), p.maxDelay.Milliseconds(), p.jitterRatio})
	return string(encoded)
}

func (p nativeRetryPolicy) delay(retry int, requested time.Duration) (time.Duration, bool) {
	if requested > 0 {
		if requested > p.maxDelay {
			return 0, false
		}
		return requested, true
	}
	base := float64(p.initialDelay)
	for index := 1; index < retry && base < float64(p.maxDelay); index++ {
		base = math.Min(base*2, float64(p.maxDelay))
	}
	random := p.random()
	delay := base * (1 - p.jitterRatio + 2*p.jitterRatio*random)
	return time.Duration(math.Min(delay, float64(p.maxDelay))), true
}

type nativeLlmFailure struct {
	Message              string `json:"message"`
	Code                 string `json:"code"`
	OffloadImages        int    `json:"offloadImages,omitempty"`
	Status               int    `json:"status,omitempty"`
	ProviderRetryAfterMs int64  `json:"providerRetryAfterMs,omitempty"`
	RequestID            string `json:"requestId,omitempty"`
}

var errNativeEmptyResponse = errors.New("model completed without content blocks")

func nativeModelFailure(err error) (nativeLlmFailure, bool) {
	failure := nativeLlmFailure{Message: err.Error(), Code: "UNKNOWN"}
	var offload *ImageOffloadRequiredError
	if errors.As(err, &offload) && offload != nil {
		failure.Code = "IMAGE_OFFLOAD_REQUIRED"
		failure.OffloadImages = offload.Count
		return failure, false
	}
	if errors.Is(err, errNativeEmptyResponse) {
		failure.Code = "EMPTY_RESPONSE"
		return failure, true
	}
	var modelErr *llm.ModelError
	if !errors.As(err, &modelErr) || modelErr == nil {
		return failure, false
	}
	failure.Status = modelErr.HTTPStatus
	failure.RequestID = modelErr.RequestID
	if modelErr.RetryAfter > 0 {
		failure.ProviderRetryAfterMs = modelErr.RetryAfter.Milliseconds()
	}
	switch modelErr.Code {
	case llm.ModelErrorUpstreamNetwork, llm.ModelErrorUpstreamStreamTruncated,
		llm.ModelErrorUpstreamTemporaryUnavailable:
		failure.Code = "TRANSPORT"
	case llm.ModelErrorUpstreamTimeout:
		failure.Code = "TIMEOUT"
	case llm.ModelErrorUpstreamRateLimited:
		failure.Code = "RATE_LIMIT"
	case llm.ModelErrorUpstreamOverloaded, llm.ModelErrorUpstreamInternal:
		failure.Code = "SERVER"
	case llm.ModelErrorResponseIncomplete:
		failure.Code = "EMPTY_RESPONSE"
	case llm.ModelErrorContextLimitExceeded:
		failure.Code = "CONTEXT_WINDOW_EXCEEDED"
	default:
		failure.Code = string(modelErr.Code)
	}
	return failure, modelErr.Retryable() && !modelErr.OutputObserved
}

func nativeTurnFailure(err error) nativeLlmFailure {
	failure, _ := nativeModelFailure(err)
	return failure
}

// completeNativeStep repeats only the model request. The queued user message,
// step boundary, and any tools stay outside this loop; a failed attempt cannot
// replay a tool side effect. Streaming models retain their real partial chunks
// in assistant/attempt; non-streaming test models truthfully retain none.
func (a *NativeLoop) completeNativeStep(ctx context.Context, turn uint64, step int, messages []Message) (modeldata.AssistantMessage, error) {
	policy := a.retryPolicy
	policyKey := policy.key()
	retryID := uuid.NewString()
	overflowCompactions := 0
	for retry := 0; ; {
		if err := ctx.Err(); err != nil {
			return modeldata.AssistantMessage{}, err
		}
		response, err := a.completeAttemptModel(ctx, turn, step, messages)
		if errors.Is(err, ErrModelCallBudget) {
			return modeldata.AssistantMessage{}, err
		}
		if err == nil && !response.MaxTokens && response.Content == "" && len(response.ToolCalls) == 0 && len(response.OutputBlocks) == 0 {
			err = errNativeEmptyResponse
		}
		if err == nil {
			return response, nil
		}
		if ctx.Err() != nil && response.StreamRecords != nil {
			content, blocks, usage := nativeInterruptedPrefix(response.StreamRecords)
			if len(blocks) > 0 {
				response.Content, response.OutputBlocks, response.ToolCalls = content, blocks, nil
				response.Interrupted = true
				if usage != nil {
					response.Usage = usage
				}
				assistantEvent, appendErr := a.logAssistant(turn, step, response)
				if appendErr != nil {
					a.endStreamAttempt(response.AttemptID, response.StreamCount, "", 0, false)
					return modeldata.AssistantMessage{}, errors.Join(err, appendErr)
				}
				if flushErr := a.session.Flush(context.Background()); flushErr != nil {
					a.endStreamAttempt(response.AttemptID, response.StreamCount, "", 0, false)
					return modeldata.AssistantMessage{}, errors.Join(err, flushErr)
				}
				a.endStreamAttempt(response.AttemptID, response.StreamCount, "assistant/message", assistantEvent.Seq, true)
				return modeldata.AssistantMessage{}, err
			}
		}
		stream := response.StreamRecords
		if stream == nil {
			stream = []any{}
		}
		attemptEvent, appendErr := a.append("assistant/attempt", map[string]any{"turn": turn, "step": step, "stream": stream}, nil, nil)
		if appendErr != nil {
			a.endStreamAttempt(response.AttemptID, response.StreamCount, "", 0, false)
			return modeldata.AssistantMessage{}, errors.Join(err, appendErr)
		}
		if flushErr := a.session.Flush(context.Background()); flushErr != nil {
			a.endStreamAttempt(response.AttemptID, response.StreamCount, "", 0, false)
			return modeldata.AssistantMessage{}, errors.Join(err, flushErr)
		}
		a.endStreamAttempt(response.AttemptID, response.StreamCount, "assistant/attempt", attemptEvent.Seq, true)
		failure, retryable := nativeModelFailure(err)
		var offload *ImageOffloadRequiredError
		if errors.As(err, &offload) && offload != nil && offload.Count > 0 && ctx.Err() == nil && response.Content == "" && len(response.ToolCalls) == 0 && response.Usage == nil {
			selected, selectErr := a.offloadOldestNativeImages(offload.Count)
			if selectErr != nil {
				return modeldata.AssistantMessage{}, errors.Join(err, selectErr)
			}
			if selected {
				if flushErr := a.session.Flush(ctx); flushErr != nil {
					return modeldata.AssistantMessage{}, errors.Join(err, flushErr)
				}
				messages, selectErr = a.modelMessages()
				if selectErr != nil {
					return modeldata.AssistantMessage{}, errors.Join(err, selectErr)
				}
				continue
			}
		}
		if failure.Code == "CONTEXT_WINDOW_EXCEEDED" && ctx.Err() == nil && response.Content == "" && len(response.ToolCalls) == 0 && response.Usage == nil {
			pruned, pruneErr := a.pruneNativeToolResults(ctx, defaultNativePruneConfig())
			if pruneErr != nil {
				return modeldata.AssistantMessage{}, errors.Join(err, pruneErr)
			}
			if pruned > 0 {
				messages, pruneErr = a.modelMessages()
				if pruneErr != nil {
					return modeldata.AssistantMessage{}, errors.Join(err, pruneErr)
				}
				continue
			}
			if overflowCompactions == 0 {
				compacted, compactErr := a.compactNativeOverflow(ctx, turn)
				if compactErr != nil {
					return modeldata.AssistantMessage{}, errors.Join(err, compactErr)
				}
				if compacted {
					overflowCompactions++
					messages, compactErr = a.modelMessages()
					if compactErr != nil {
						return modeldata.AssistantMessage{}, errors.Join(err, compactErr)
					}
					continue
				}
			}
		}
		if ctx.Err() != nil || !retryable || response.Content != "" || len(response.ToolCalls) != 0 || response.Usage != nil || retry >= policy.maxRetries {
			return modeldata.AssistantMessage{}, err
		}
		delay, allowed := policy.delay(retry+1, time.Duration(failure.ProviderRetryAfterMs)*time.Millisecond)
		if !allowed {
			return modeldata.AssistantMessage{}, err
		}
		if _, appendErr := a.append("llm/retry", map[string]any{
			"retryId": retryID, "turn": turn, "step": step, "provider": a.provider,
			"mode": "normal", "policyKey": policyKey, "retry": retry + 1,
			"maxRetries": policy.maxRetries, "delayMs": delay.Milliseconds(), "failure": failure,
		}, nil, nil); appendErr != nil {
			return modeldata.AssistantMessage{}, errors.Join(err, appendErr)
		}
		if flushErr := a.session.Flush(ctx); flushErr != nil {
			return modeldata.AssistantMessage{}, errors.Join(err, flushErr)
		}
		if waitErr := policy.wait(ctx, delay); waitErr != nil {
			return modeldata.AssistantMessage{}, waitErr
		}
		if ctx.Err() != nil {
			return modeldata.AssistantMessage{}, ctx.Err()
		}
		if _, appendErr := a.append("llm/retry-started", map[string]any{
			"retryId": retryID, "turn": turn, "step": step, "retry": retry + 1,
		}, nil, nil); appendErr != nil {
			return modeldata.AssistantMessage{}, errors.Join(err, appendErr)
		}
		if flushErr := a.session.Flush(ctx); flushErr != nil {
			return modeldata.AssistantMessage{}, errors.Join(err, flushErr)
		}
		retry++
	}
}
