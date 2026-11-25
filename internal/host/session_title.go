package host

import (
	"context"
	"errors"

	"go-bot/harness/agent"
	"go-bot/harness/sessiontitle"
)

func (m *hostModels) generateTitle(ctx context.Context, route agent.ModelRoute, text string) (string, error) {
	m.mu.RLock()
	model, err := m.selectedLocked(route)
	m.mu.RUnlock()
	if err != nil {
		return "", err
	}
	// Reuse the chosen endpoint's actual adapter, protocol and authentication.
	// This request has no conversation surface or executable tools.
	ctx = agent.WithModelRoute(ctx, route)
	ctx = agent.WithModelCallOptions(ctx, agent.ModelCallOptions{Purpose: "session-title", MaxOutputTokens: sessiontitle.MaxOutputTokens})
	result, err := model.Complete(ctx, []agent.Message{
		{Role: "system", Content: sessiontitle.System},
		{Role: "user", Content: text},
	}, nil)
	if err != nil {
		return "", err
	}
	if result.MaxTokens || len(result.ToolCalls) > 0 {
		return "", errors.New("invalid title response")
	}
	return result.Content, nil
}
