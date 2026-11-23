package plugins

import (
	"encoding/json"
	"go-bot/plugin"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreparationActivatesSuccessfulPackageAndSkipsItsReinstallation(t *testing.T) {
	c := testRuntimeConfig(t)
	c.Workspace = t.TempDir()
	directory := t.TempDir()
	root := filepath.Join(directory, "profile", "node_modules", "fixture")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"package.json": `{"name":"fixture","version":"0.1.0","type":"module","exports":"./index.mjs","goBot":{"api":"1"}}`,
		"index.mjs":    `export const inject=['host'];export async function apply(ctx){await ctx.host.tools.register({name:'echo',description:'Ready',inputSchema:{type:'object'},async execute(){return{content:[{type:'text',text:'ready'}]}}})}`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(directory, "installed.json")
	count := filepath.Join(directory, "installs")
	started, release := filepath.Join(directory, "started"), filepath.Join(directory, "release")
	_ = os.WriteFile(state, []byte("[]"), 0600)
	paths, _ := json.Marshal([]string{state, count, started, release})
	main := filepath.Join(directory, "packages.mjs")
	script := `import{readFile,writeFile,appendFile,access}from'node:fs/promises';const[state,count,started,release]=` + string(paths) + `;
let text='';for await(const chunk of process.stdin)text+=chunk;const req=JSON.parse(text);let installed=JSON.parse(await readFile(state,'utf8'));let result=installed;
if(req.method==='install'){
 await writeFile(started,'');
 while(true){try{await access(release);break}catch{await new Promise(resolve=>setTimeout(resolve,10))}}
 if(req.name==='bad'){process.stdout.write(JSON.stringify({error:{code:'INSTALL_FAILED',message:'intentional download failure'}}));process.exit(0)}
 result={name:req.name,version:'0.1.0',api:'1',present:true,source:req.source};installed.push(result);await writeFile(state,JSON.stringify(installed));await appendFile(count,'installed\n')
}process.stdout.write(JSON.stringify({result}));`
	if err := os.WriteFile(main, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(t.Context(), ManagerConfig{Directory: directory, Runtime: c, PackageMain: main})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	s := plugin.RuntimeSettings{Packages: map[string]plugin.RuntimePackage{
		"fixture": {Source: plugin.Source{Type: "directory", Spec: root}}, "bad": {Source: plugin.Source{Type: "directory", Spec: root}},
	}, Instances: []plugin.InstanceConfig{{ID: "fixture", Package: "fixture", Enabled: true}, {ID: "bad", Package: "bad", Enabled: true}}}
	for attempt := range 2 {
		done := make(chan error, 1)
		go func() { _, err := m.Apply(t.Context(), s); done <- err }()
		if attempt == 0 {
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(started); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("default installation did not start")
				}
				time.Sleep(10 * time.Millisecond)
			}
			// Both defaults must show installing, including the queued sibling,
			// before any Cordis instance exists. Status is the HTTP projection.
			status, err := m.Status(t.Context())
			if err != nil || len(status.Instances) != 2 {
				t.Fatalf("missing initial progress: %+v %v", status, err)
			}
			for _, row := range status.Instances {
				if row.State != "installing" || row.Error != "" {
					t.Fatalf("default misreported before installation finished: %+v", row)
				}
			}
			if err := os.WriteFile(release, nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := <-done; err == nil || !strings.Contains(err.Error(), "intentional download failure") {
			t.Fatalf("missing failure: %v", err)
		}
		status, err := m.Status(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range status.Instances {
			if row.Package == "bad" && (row.State != "installation-failed" || !strings.Contains(row.Error, "intentional download failure")) {
				t.Fatalf("installation error not reported: %+v", row)
			}
			if row.Package == "fixture" && row.State != "ready" {
				t.Fatalf("successful default not ready: %+v", row)
			}
		}
		tools, err := m.Tools(t.Context(), "session")
		if err != nil || len(tools) != 1 || tools[0].Spec().Name != "fixture__echo" {
			t.Fatalf("successful package not usable: %v %v", tools, err)
		}
	}
	data, err := os.ReadFile(count)
	if err != nil || string(data) != "installed\n" {
		t.Fatalf("successful install reran: %q %v", data, err)
	}
}
