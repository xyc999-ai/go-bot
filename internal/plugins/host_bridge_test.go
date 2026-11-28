package plugins

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-bot/harness/agent"
	"go-bot/internal/authorization"
	"go-bot/internal/credentials"
	"go-bot/internal/modeldata"
)

func TestHostBridgeRealNodeDirectToolAndNativeCordisDisposal(t *testing.T) {
	c := testRuntimeConfig(t)

	c.Workspace = t.TempDir()
	store, err := credentials.Open(filepath.Join(t.TempDir(), "credentials.yaml"), credentials.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	c.Credentials = store
	c.Authorization = authorization.New(store)
	_, err = store.ModifyRecord(t.Context(), "work/mailbox", func(*credentials.Record) (*credentials.Record, error) {
		key := "example-secret"
		return &credentials.Record{Kind: "api-key", Key: &key}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(c.ProfileDir, "node_modules", "example-mail")
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"package.json": `{"name":"example-mail","version":"0.1.0","type":"module","exports":"./index.mjs","goBot":{"api":"1"}}`,
		"index.mjs":    `export const name='mail';export const inject=['host'];export async function apply(ctx){await ctx.host.tools.register({name:'echo',description:'Echo credential presence',inputSchema:{type:'object'},async execute(args,call){const key=await ctx.host.credentials.get('mailbox');return{content:[{type:'text',text:call.sessionId+':'+(key.key?'configured':'missing')}]}}})}`,
	} {
		if err = os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := startHostBridge(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	status, err := r.Apply(t.Context(), []hostPluginInstance{{ID: "work", Package: "example-mail", Enabled: true}})
	if err != nil || len(status) != 1 || status[0]["state"] != "ready" {
		t.Fatalf("load: %v %v", status, err)
	}
	tools, err := r.Tools(t.Context(), "session-one")
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	if tools[0].Spec().Name != "work__echo" {
		t.Fatal(tools[0].Spec().Name)
	}
	result, err := tools[0].(interface {
		ExecuteResult(context.Context, json.RawMessage) (modeldata.ToolResult, error)
	}).ExecuteResult(t.Context(), json.RawMessage(`{}`))
	if err != nil || len(result.ContentBlocks) != 1 || result.ContentBlocks[0].Text != "session-one:configured" {
		t.Fatalf("tool: %+v %v", result, err)
	}
	if _, err = r.Apply(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if tools, err = r.Tools(t.Context(), "session-one"); err != nil || len(tools) != 0 {
		t.Fatalf("native Cordis cleanup did not remove tools: %v %v", tools, err)
	}
	if record, readErr := store.ReadRecord("work/mailbox"); readErr != nil || record == nil {
		t.Fatalf("plugin unload removed a durable credential: %v", readErr)
	}
}

func bridgeFixture(t *testing.T, source string, configure ...func(*RuntimeConfig)) *hostBridge {
	t.Helper()
	c := testRuntimeConfig(t)

	c.Workspace = t.TempDir()
	store, err := credentials.Open(filepath.Join(t.TempDir(), "credentials.yaml"), credentials.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	c.Credentials = store
	c.Authorization = authorization.New(store)
	root := filepath.Join(c.ProfileDir, "node_modules", "fixture")
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"package.json": `{"name":"fixture","version":"0.1.0","type":"module","exports":"./index.mjs","goBot":{"api":"1"}}`, "index.mjs": source} {
		if err = os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, apply := range configure {
		apply(&c)
	}
	r, err := startHostBridge(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestHostBridgeCredentialUpdateIsAtomicAndRejectsReentrantWrites(t *testing.T) {
	r := bridgeFixture(t, `export const name='credentials';export const inject=['host'];export async function apply(ctx){await ctx.host.credentials.put('account',{kind:'grant',payload:{count:0}});await ctx.host.tools.register({name:'update',description:'Update credential',inputSchema:{type:'object'},async execute(){const saved=await ctx.host.credentials.update('account',async current=>{const observed=await ctx.host.credentials.get('account');if(observed.payload.count!==0)throw Error('unexpected snapshot');return{kind:'grant',payload:{count:current.payload.count+1}}});let failure='';try{await ctx.host.credentials.update('account',async current=>{await ctx.host.credentials.put('other',{kind:'api-key',key:'bad'});return current})}catch(error){failure=error.code}return{content:[{type:'text',text:saved.payload.count+':'+failure}]}}})}`)
	status, err := r.Apply(t.Context(), []hostPluginInstance{{ID: "account", Package: "fixture", Enabled: true}})
	if err != nil || len(status) != 1 || status[0]["state"] != "ready" {
		t.Fatalf("apply: %v %v", status, err)
	}
	tools, err := r.Tools(t.Context(), "session")
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	text, err := tools[0].Execute(t.Context(), json.RawMessage(`{}`))
	if err != nil || text != "1:CONFLICT" {
		t.Fatalf("credential callback: %q %v", text, err)
	}
	other, err := r.config.Credentials.ReadRecord("account/other")
	if err != nil || other != nil {
		t.Fatalf("nested write reached storage: %v %v", other, err)
	}
}

func TestHostBridgeAuthorizationUsesOnlyGoFlowAndCredentialServices(t *testing.T) {
	r := bridgeFixture(t, `export const name='login';export const inject=['host'];export async function apply(ctx){await ctx.host.authorization.register({key:'mailbox',title:'Mailbox',methods:[{id:'code',label:'Use code'}],async run(session){await session.notify({message:'Continue login'});const token=await session.prompt({kind:'secret',message:'Access code'});await session.commit({kind:'grant',payload:{access_token:token}})}})}`)
	status, err := r.Apply(t.Context(), []hostPluginInstance{{ID: "login", Package: "fixture", Enabled: true}})
	if err != nil || len(status) != 1 || status[0]["state"] != "ready" {
		t.Fatalf("apply: %v %v", status, err)
	}
	notices := 0
	result, err := r.config.Authorization.Begin(t.Context(), "login/mailbox", "code", authorization.Interaction{
		Notify: func(context.Context, authorization.Notice) error { notices++; return nil },
		Prompt: func(context.Context, authorization.Prompt) (string, error) { return "test-access-token", nil },
	})
	if err != nil || result.Status != "authorized" || notices != 1 {
		t.Fatalf("authorization: %+v notices=%d %v", result, notices, err)
	}
	record, err := r.config.Credentials.ReadRecord("login/mailbox")
	if err != nil || record == nil || !strings.Contains(string(record.Payload), "test-access-token") {
		t.Fatalf("authorization did not commit to Go credentials: %+v %v", record, err)
	}
	if _, err = r.Apply(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if len(r.config.Authorization.List()) != 0 {
		t.Fatal("Cordis disposal retained a Go authorization registration")
	}
}

func TestHostBridgeAuthorizationBrowserCallbackUsesPromiseRPC(t *testing.T) {
	r := bridgeFixture(t, `export const inject=['host'];export async function apply(ctx){await ctx.host.authorization.register({key:'account',title:'Browser account',methods:[{id:'oauth',label:'Connect in browser'}],async run(session){const callback=await session.callback({state:'expected-state'});await session.notify({message:'Open provider',url:callback.url});const query=await callback.wait();if(query.code?.[0]!=='fixture-code')throw Error('Provider response rejected');await session.commit({kind:'grant',payload:{access_token:'fixture-token'}})}})}`)
	if _, err := r.Apply(t.Context(), []hostPluginInstance{{ID: "login", Package: "fixture", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	surface := authorization.NewSurface(t.Context(), r.config.Authorization, nil)
	t.Cleanup(surface.Close)
	ctx := authorization.WithCallbackBase(t.Context(), "https://controller.example/v1/plugins/authorization/callback/alice/")
	value, err := surface.Handle(ctx, "start", json.RawMessage(`{"key":"login/account"}`))
	if err != nil {
		t.Fatal(err)
	}
	view := value.(authorization.View)
	deadline := time.Now().Add(3 * time.Second)
	for len(view.Notices) == 0 && time.Now().Before(deadline) {
		value, err = surface.Handle(t.Context(), "get", json.RawMessage(`{"id":"`+view.ID+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		view = value.(authorization.View)
		time.Sleep(time.Millisecond)
	}
	if len(view.Notices) != 1 {
		t.Fatalf("Node did not prepare callback: %+v", view)
	}
	u, err := url.Parse(view.Notices[0].URL)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"token": u.Path[strings.LastIndex(u.Path, "/")+1:], "query": map[string][]string{"state": {"expected-state"}, "code": {"fixture-code"}}})
	if _, err = surface.Handle(t.Context(), "callback", input); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		value, err = surface.Handle(t.Context(), "get", json.RawMessage(`{"id":"`+view.ID+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		view = value.(authorization.View)
		if view.Status != "running" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if view.Status != "authorized" {
		t.Fatalf("Node callback failed: %+v", view)
	}
	record, err := r.config.Credentials.ReadRecord("login/account")
	if err != nil || record == nil || !strings.Contains(string(record.Payload), "fixture-token") {
		t.Fatal("no Go credential commit", record, err)
	}
}

func bridgeRunTool(t *testing.T, r *hostBridge, name string, approve func(context.Context, agent.ToolPolicyAsk) error) agent.ToolPipelineResult {
	t.Helper()
	tools, err := r.Tools(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	var target agent.Tool
	for _, tool := range tools {
		if tool.Spec().Name == name {
			target = tool
			break
		}
	}
	if target == nil {
		t.Fatal("missing tool", name)
	}
	p, err := hostToolInvocation(t.Context(), agent.ToolExecution{SessionID: "session", CallID: "recorded-call", Name: name, CWD: r.config.Workspace, Arguments: json.RawMessage(`{}`)}, func(ctx context.Context) modeldata.ToolResult {
		value, err := target.(interface {
			ExecuteResult(context.Context, json.RawMessage) (modeldata.ToolResult, error)
		}).ExecuteResult(ctx, json.RawMessage(`{}`))
		if err != nil {
			return modeldata.ToolResult{IsError: true, Content: err.Error()}
		}
		return value
	}, approve)
	if err != nil {
		t.Fatal(err)
	}
	prepared := p.Prepare()
	outcome := prepared.Outcome
	if prepared.Dispatch {
		outcome = p.Dispatch()
	}
	return p.Complete(outcome)
}

func TestHostBridgeCredentialQueriesUseActualStoreAndInstanceAddresses(t *testing.T) {
	r := bridgeFixture(t, `export const inject=['host'];export async function apply(ctx){
 await ctx.host.credentials.put('account',{kind:'api-key',key:'fixture-key'});
 await ctx.host.tools.register({name:'inspect',description:'Inspect stored credentials',inputSchema:{type:'object'},async execute(){
  const info=await ctx.host.info(),description=await ctx.host.credentials.describe('account'),entries=await ctx.host.credentials.list();
  const ref=await ctx.host.credentials.resolve('SHARED_REF'),missing=await ctx.host.credentials.get('missing');
  const saved=await ctx.host.credentials.update('account',async()=>null);
  await ctx.host.credentials.remove('account');
  return {content:[{type:'text',text:JSON.stringify({info,description,entries,ref,missing,saved,after:await ctx.host.credentials.get('account')})}]};
 }});
}`, func(c *RuntimeConfig) {
		c.UserID, c.HostID = "fixture-user", "fixture-host"
		if err := c.Credentials.Set(t.Context(), "SHARED_REF", "fixture-shared-value"); err != nil {
			t.Fatal(err)
		}
		other := "other-instance-key"
		if _, err := c.Credentials.ModifyRecord(t.Context(), "other/account", func(*credentials.Record) (*credentials.Record, error) {
			return &credentials.Record{Kind: "api-key", Key: &other}, nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := r.Apply(t.Context(), []hostPluginInstance{{ID: "mail", Package: "fixture", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	result := bridgeRunTool(t, r, "mail__inspect", nil)
	decoded, err := agent.DecodeToolPipelineResult(result)
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Info           struct{ UserID, HostID, DataRoot, APIVersion string }
		Description    credentials.RecordInfo
		Entries        []credentials.RecordEntry
		Ref            string
		Missing, After *credentials.Record
		Saved          *credentials.Record
	}
	if err = json.Unmarshal([]byte(decoded.Content), &view); err != nil {
		t.Fatal(err)
	}
	if view.Info.UserID != "fixture-user" || view.Info.HostID != "fixture-host" || view.Info.DataRoot != r.config.Workspace || view.Info.APIVersion != "1" {
		t.Fatalf("info: %+v", view.Info)
	}
	if !view.Description.Configured || !view.Description.Writable || view.Description.Kind != "api-key" || len(view.Entries) != 1 || view.Entries[0].Key != "account" {
		t.Fatalf("view: %+v", view)
	}
	if view.Ref != "fixture-shared-value" || view.Missing != nil || view.After != nil || view.Saved == nil || view.Saved.Key == nil || *view.Saved.Key != "fixture-key" {
		t.Fatalf("store result: %+v", view)
	}
	if strings.Contains(decoded.Content, "other-instance-key") {
		t.Fatal("foreign credential entered the plugin view")
	}
	if record, err := r.config.Credentials.ReadRecord("mail/account"); err != nil || record != nil {
		t.Fatal("remove did not update Go store", err)
	}
}

func TestHostBridgePassesNativeLoopbackPromptToItsOwnAuthorization(t *testing.T) {
	r := bridgeFixture(t, `export const inject=['host'];export async function apply(ctx){await ctx.host.authorization.register({key:'account',title:'Native account',methods:[{id:'oauth',label:'Connect'}],async run(session){const value=await session.prompt({kind:'text',message:'Paste callback',loopback:{authorizationUrl:'https://example.test/login?redirect_uri=http%3A%2F%2Flocalhost%3A8082%2Fcallback&state=one-state',redirectUri:'http://localhost:8082/callback',state:'one-state'}});if(value!=='http://localhost:8082/callback?code=fixture&state=one-state')throw Error('Wrong callback answer');await session.commit({kind:'grant',payload:{access_token:'synthetic-only'}})}})}`)
	if _, err := r.Apply(t.Context(), []hostPluginInstance{{ID: "native-plugin", Package: "fixture", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	result, err := r.config.Authorization.Begin(t.Context(), "native-plugin/account", "oauth", authorization.Interaction{Prompt: func(_ context.Context, p authorization.Prompt) (string, error) {
		if p.Loopback == nil || p.Loopback.RedirectURI != "http://localhost:8082/callback" || p.Loopback.State != "one-state" {
			t.Fatalf("native callback metadata lost: %+v", p)
		}
		return "http://localhost:8082/callback?code=fixture&state=one-state", nil
	}})
	if err != nil || result.Status != "authorized" {
		t.Fatalf("native callback: %+v %v", result, err)
	}
	record, err := r.config.Credentials.ReadRecord("native-plugin/account")
	if err != nil || record == nil {
		t.Fatalf("grant missing: %v", err)
	}
	if other, _ := r.config.Credentials.ReadRecord("other-plugin/account"); other != nil {
		t.Fatal("callback reached another plugin")
	}
}
