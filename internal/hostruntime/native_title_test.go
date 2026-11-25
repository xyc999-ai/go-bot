package hostruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"go-bot/harness/agent"
	"go-bot/harness/assembly"
)

func TestNativeRuntimeTitlesFollowClaimedUserMessagesAndManualRename(t *testing.T) {
	store := &nativeRuntimeStore{}
	generated := make(chan struct{}, 1)
	worker := &NativeRuntime{
		Factory:  nativeRuntimeTestFactory(t, store, nativeRuntimeModel{}),
		Identity: assembly.NativeRuntimeIdentity{UserID: "alice", SessionID: "native-title", Workspace: "/workspace"},
		TitleGenerator: func(_ context.Context, route agent.ModelRoute, input string) (string, error) {
			if route.Provider != "test-provider" || route.Model != "test-model" || !strings.Contains(input, "整理行业报告") {
				t.Error("wrong actual request route/input", route, input)
			}
			generated <- struct{}{}
			return "行业报告整理", nil
		},
	}
	if err := worker.Start(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	if _, err := worker.SubmitText(t.Context(), "整理行业报告"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-generated:
	case <-time.After(time.Second):
		t.Fatal("first claimed user message did not trigger naming")
	}
	deadline := time.Now().Add(time.Second)
	for {
		state, err := worker.live.Session.TitleState()
		if err != nil {
			t.Fatal(err)
		}
		if state.Current != nil && state.Current.Source.Kind == "provider" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("generated title did not commit")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := worker.RenameTitle(t.Context(), "自己的标题"); err != nil {
		t.Fatal(err)
	}
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	resumed := &NativeRuntime{Factory: nativeRuntimeTestFactory(t, store, nativeRuntimeModel{}), Identity: worker.Identity,
		TitleGenerator: func(context.Context, agent.ModelRoute, string) (string, error) {
			t.Error("resume regenerated a pinned title")
			return "wrong", nil
		},
	}
	if err := resumed.Start(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if _, err := resumed.SubmitText(t.Context(), "后续交流"); err != nil {
		t.Fatal(err)
	}
	if err := resumed.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := resumed.live.Session.TitleState()
	if err != nil || state.Current.Title != "自己的标题" || state.Current.Source.Kind != "user" {
		t.Fatal(state, err)
	}
}
