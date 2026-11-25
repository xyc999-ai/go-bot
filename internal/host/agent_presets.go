package host

import (
	"context"
	"go-bot/harness/agentpreset"
	"go-bot/internal/proactive"
)

// One registry serves ordinary and background Agents inside this user Host.
// The background id remains available only through its internal activation path.
func newHostPresetRegistry(extras ...func() agentpreset.Catalog) *agentpreset.Registry {
	r := agentpreset.New(agentpreset.Standard, func(_ context.Context, d agentpreset.Definition) error {
		catalog := agentpreset.Catalog{Sources: []agentpreset.SourceRef{"builtin:tools", "builtin:files", "builtin:skills", "builtin:filesystem-skills", "builtin:memory", "builtin:session-search", "builtin:time", "builtin:persona", "builtin:plan", "builtin:structured-output"}, Tools: map[string]agentpreset.SourceRef{}}
		for _, name := range []string{"readfile", "writefile", "editfile", "listfiles", "glob", "grep", "shellexecute", "job_output", "job_list", "job_kill", "skill", "present", "readimage", "web_search", "web_fetch", "memory", "session_search", "structured_output", "todowrite", "askuserquestion", "ask_user_question", "exitplanmode", "exit_plan_mode", "get_goal", "create_goal", "update_goal", "subagent", "subagent_fork", "send_message", "interrupt_agent", "list_agents", "workflow", "schedule_create", "schedule_list", "schedule_view", "schedule_edit"} {
			source := agentpreset.BuiltinToolSource(name)
			catalog.Tools[name] = source
			catalog.Sources = append(catalog.Sources, source)
		}
		for _, extra := range extras {
			c := extra()
			catalog.Sources = append(catalog.Sources, c.Sources...)
			catalog.LocalTools = append(catalog.LocalTools, c.LocalTools...)
			for name, source := range c.Tools {
				catalog.Tools[name] = source
			}
		}
		return catalog.Check(d)
	})
	for _, definition := range []agentpreset.Definition{{ID: agentpreset.Standard, Name: "Standard"}, proactive.PresetDefinition()} {
		if _, err := r.Register(definition); err != nil {
			panic(err)
		}
	}
	return r
}
