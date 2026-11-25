package host

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"os"
	"time"

	"go-bot/extensions/catalog"
	"go-bot/harness/agentpreset"
	"go-bot/harness/assembly"
	"go-bot/harness/session"
	"go-bot/internal/durablefs"
)

func mainWelcome(locale string) string {
	mediaName := ""
	for _, release := range catalog.Entries() {
		if release.ID == "gemini-api" {
			mediaName = release.Name
			break
		}
	}
	if locale == "zh-CN" {
		text := "你好，我是 Go Bot，你的个人 AI 助手。\n\n日常事务或工作上的事情，都可以交给我一起处理：查找资料、操作浏览器，或者制作和编辑 Word、PPT、Excel 文件。你可以直接说想完成什么，我们再一起把细节理清楚。"
		if mediaName != "" {
			text += "\n\n想生成图片或视频？可以到插件市场安装并启用 **" + mediaName + "**，配置自己的 API Key 后再交给我处理。你也可以连接更多插件，扩展我能帮你做的事情。"
		}
		return text + "\n\n今天有什么想让我帮忙的？"
	}
	text := "Hi, I'm Go Bot, your personal AI assistant.\n\nI can help with everyday tasks and work—researching a topic, using the browser, or creating and editing Word documents, presentations, and spreadsheets. Tell me what you'd like to get done, and we can work through the details together."
	if mediaName != "" {
		text += "\n\nWant to create images or videos? Install and enable **" + mediaName + "** from the plugin market, then connect your own API key. You can connect more plugins whenever you want to expand what I can help with."
	}
	return text + "\n\nWhat would you like a hand with today?"
}

func ensureMainJournal(ctx context.Context, root, id, locale string, claim sessionformat.OwnerClaim, presetIDs ...string) error {
	if _, err := findHostSession(ctx, root, id); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	workspace := defaultSessionWorkspace(root, id)
	if err := durablefs.EnsureDirectory(workspace); err != nil {
		return err
	}
	store := sessionformat.FileStore{Root: sessionLogRoot(root), ProjectCWD: workspace, Claim: claim}
	now := time.Now().UnixMilli()
	message, err := json.Marshal(map[string]any{"stream": []any{}, "message": map[string]any{
		"id": uuid.NewString(), "role": "assistant", "source": map[string]string{"kind": "go-bot/welcome"}, "content": []map[string]string{{"type": "text", "text": mainWelcome(locale)}},
	}})
	if err != nil {
		return err
	}
	preset := agentpreset.Standard
	if len(presetIDs) > 0 {
		preset = presetIDs[0]
	}
	header := sessionformat.Header{Type: "session", Version: sessionformat.Version, ID: id, CreatedAt: now, CWD: workspace, AgentPreset: preset}
	system, err := json.Marshal(map[string]any{"message": map[string]any{
		"id": uuid.NewString(), "role": "system", "source": map[string]string{"kind": "go-bot/initial-system"},
		"content": []map[string]string{{"type": "text", "text": assembly.DefaultCoreSystemPrompt}},
	}})
	if err != nil {
		return err
	}
	prepared, err := sessionformat.PrepareSession(header, []sessionformat.Event{
		{Type: "system/message", Seq: 0, Time: now, Data: system, SurfaceOp: json.RawMessage(`"append"`)},
		{Type: "assistant/message", Seq: 1, Time: now, Data: message, SurfaceOp: json.RawMessage(`"append"`)},
	}, nil)
	if err != nil {
		return err
	}
	journal, err := store.CreatePrepared(ctx, prepared.Header(), prepared.Events())
	if err != nil {
		return err
	}
	return journal.Close()
}
