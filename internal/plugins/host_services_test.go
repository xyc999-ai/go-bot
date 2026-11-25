package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-bot/harness/agent"
	"go-bot/harness/agentpreset"
	"go-bot/harness/skills"
)

func TestPresetFiltersPluginPromptContributionsBeforeCallingNode(t *testing.T) {
	r := bridgeFixture(t, `export const name='prompt';export const inject=['host'];export async function apply(ctx){
 await ctx.host.prompts.register({id:'system',location:'system',text:()=>{throw new Error('excluded system callback ran')}});
 await ctx.host.prompts.register({id:'runtime',location:'context',text:()=>{throw new Error('excluded context callback ran')}});
}`)
	if _, err := r.Apply(t.Context(), []hostPluginInstance{{ID: "prompt", Package: "fixture", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	registry := agentpreset.New("research", nil)
	_, err := registry.Register(agentpreset.Definition{ID: "research", PromptSections: agentpreset.Selection{DenySources: []agentpreset.SourceRef{"plugin:prompt"}}, RuntimeSources: agentpreset.Selection{DenySources: []agentpreset.SourceRef{"plugin:prompt"}}})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := registry.Resolve(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.AssemblePrompt(agentpreset.WithBinding(t.Context(), binding), agent.PromptAssemblyInput{SystemPrompt: "Go core"})
	if err != nil || out.SystemPrompt != "Go core" || len(out.Contexts) != 0 {
		t.Fatalf("filtered prompt: %+v %v", out, err)
	}
}

func TestHostBridgeSkillsUseGoDiscoveryAndProviderCallbacks(t *testing.T) {
	r := bridgeFixture(t, `export const name='skills';export const inject=['host'];export async function apply(ctx){
 await ctx.host.credentials.put('lookup',{kind:'grant',payload:{name:'remote-skill'}});
 await ctx.host.skills.register({name:'shared-skill',description:'Plugin version',content:'plugin body'});
 await ctx.host.skills.registerDirectory('skills');
 await ctx.host.skills.registerProvider({id:'remote',async list(context){
   const stored=await ctx.host.credentials.get('lookup');
   let blocked=false;try{await ctx.host.skills.list()}catch(error){blocked=error.code==='CONFLICT'}
   if(!blocked)throw Error('reentrant discovery was allowed');
   return{items:[{name:stored.payload.name,description:'Remote summary',key:{id:7}}],complete:true};
 },async get(key,context){if(key.id!==7)throw Error('locator changed');return{name:'remote-skill',description:'Remote summary',content:'loaded for '+context.cwd}}});
 await ctx.host.tools.register({name:'inspect',description:'Inspect Go Skill registry',inputSchema:{type:'object'},async execute(){
   await ctx.host.skills.invalidate();const listed=await ctx.host.skills.list();const loaded=await ctx.host.skills.get('remote-skill');
   return{content:[{type:'text',text:JSON.stringify({listed,loaded})}]};
 }});
}`, func(c *RuntimeConfig) {
		c.Skills = &skills.Registry{DSHHome: filepath.Join(c.Workspace, ".dsh")}
		files := map[string]string{
			filepath.Join(c.Skills.DSHHome, "skills", "shared-skill", "SKILL.md"):                         "---\nname: shared-skill\ndescription: User version\n---\nuser body",
			filepath.Join(c.ProfileDir, "node_modules", "fixture", "skills", "bundled-skill", "SKILL.md"): "---\nname: bundled-skill\ndescription: Package directory\n---\nbundled body",
		}
		for path, data := range files {
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
	})
	r.config.Skills.Providers = r.Skills
	status, err := r.Apply(t.Context(), []hostPluginInstance{{ID: "skills", Package: "fixture", Enabled: true}})
	if err != nil || status[0]["state"] != "ready" {
		t.Fatalf("load: %v %v", status, err)
	}
	lookup := skills.Lookup{CWD: r.config.Workspace, Workspace: r.config.Workspace}
	snapshot, err := r.config.Skills.Snapshot(t.Context(), lookup)
	if err != nil || !snapshot.Complete || len(snapshot.Entries) != 3 {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	for _, name := range []string{"shared-skill", "bundled-skill", "remote-skill"} {
		candidate, found, err := r.config.Skills.Find(t.Context(), lookup, name)
		if err != nil || !found {
			t.Fatalf("find %s: %v %v", name, found, err)
		}
		loaded, found, err := candidate.Load(t.Context())
		if err != nil || !found {
			t.Fatalf("load %s: %v %v", name, found, err)
		}
		if name == "shared-skill" && loaded.Value.Content != "user body" {
			t.Fatalf("plugin shadowed user's skill: %+v", loaded)
		}
		if name == "remote-skill" && loaded.Value.Content != "loaded for "+lookup.CWD {
			t.Fatal(loaded)
		}
	}

	updated := "---\nname: bundled-skill\ndescription: Updated package directory\n---\nupdated directory body"
	if err := os.WriteFile(filepath.Join(r.config.ProfileDir, "node_modules", "fixture", "skills", "bundled-skill", "SKILL.md"), []byte(updated), 0600); err != nil {
		t.Fatal(err)
	}
	candidate, found, err := r.config.Skills.Find(t.Context(), lookup, "bundled-skill")
	if err != nil || !found {
		t.Fatal("updated directory not discovered", err)
	}
	loaded, found, err := candidate.Load(t.Context())
	if err != nil || !found || loaded.Value.Content != "updated directory body" {
		t.Fatalf("stale directory: %+v %v", loaded, err)
	}
	tools, err := r.Tools(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	text, err := tools[0].Execute(t.Context(), json.RawMessage(`{}`))
	if err != nil || !strings.Contains(text, `"complete":true`) || !strings.Contains(text, "loaded for ") {
		t.Fatalf("nested Go discovery: %s %v", text, err)
	}
	if _, err = r.Apply(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	snapshot, err = r.config.Skills.Snapshot(t.Context(), lookup)
	if err != nil || len(snapshot.Entries) != 1 || snapshot.Entries[0].Name != "shared-skill" {
		t.Fatalf("unload: %+v %v", snapshot, err)
	}
}

func TestHostBridgePromptIsAppendedInGoAndReflectsCurrentInput(t *testing.T) {
	r := bridgeFixture(t, `export const name='prompt';export const inject=['host'];export async function apply(ctx){
 await ctx.host.prompts.register({id:'last',location:'system',order:9,text:'last'});
 await ctx.host.prompts.register({id:'dynamic',location:'context',text:async context=>'cwd='+context.cwd+';model='+context.model.name});
 await ctx.host.prompts.register({id:'first',location:'system',order:-1,text:'first'});
}`)
	status, err := r.Apply(t.Context(), []hostPluginInstance{{ID: "prompt", Package: "fixture", Enabled: true}})
	if err != nil || status[0]["state"] != "ready" {
		t.Fatalf("load: %v %v", status, err)
	}
	for _, model := range []string{"one", "two"} {
		out, err := r.AssemblePrompt(t.Context(), agent.PromptAssemblyInput{SessionID: "session", CWD: r.config.Workspace, SystemPrompt: "core memory + skills", Tools: []agent.ToolSpec{{Name: "shellexecute"}}, ModelSelection: agent.ModelRoute{Provider: "main", Model: model}})
		if err != nil || out.SystemPrompt != "core memory + skills\n\nfirst\n\nlast" || len(out.Tools) != 1 || len(out.Contexts) != 1 || out.Contexts[0].Text != "cwd="+r.config.Workspace+";model="+model {
			t.Fatalf("assembly: %+v %v", out, err)
		}
	}
	if _, err = r.Apply(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	out, err := r.AssemblePrompt(t.Context(), agent.PromptAssemblyInput{SystemPrompt: "core"})
	if err != nil || out.SystemPrompt != "core" || len(out.Contexts) != 0 {
		t.Fatalf("unload: %+v %v", out, err)
	}
}
