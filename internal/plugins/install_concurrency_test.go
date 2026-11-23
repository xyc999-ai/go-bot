package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-bot/plugin"
)

func TestNewPackageInstallationKeepsExistingCordisToolsAvailable(t *testing.T) {
	c := testRuntimeConfig(t)
	c.Workspace = t.TempDir()
	directory := t.TempDir()
	root := filepath.Join(directory, "profile", "node_modules", "fixture")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"package.json": `{"name":"fixture","version":"0.1.0","type":"module","exports":"./index.mjs","goBot":{"api":"1"}}`,
		"index.mjs":    `export const inject=['host'];export async function apply(ctx){await ctx.host.tools.register({name:'echo',description:'Existing working tool',inputSchema:{type:'object'},async execute(){return{content:[{type:'text',text:'ready'}]}}})}`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	base := plugin.RuntimePackage{Source: plugin.Source{Type: "directory", Spec: root}, Version: "0.1.0"}
	data, _ := json.Marshal([]plugin.PackageInfo{{Name: "fixture", Version: "0.1.0", API: "1", Present: true, Source: base.Source}})
	state, started, release := filepath.Join(directory, "installed.json"), filepath.Join(directory, "started"), filepath.Join(directory, "release")
	if err := os.WriteFile(state, data, 0600); err != nil {
		t.Fatal(err)
	}
	paths, _ := json.Marshal([]string{state, started, release})
	main := filepath.Join(directory, "packages.mjs")
	script := `import {readFile,writeFile,access} from 'node:fs/promises';
const [state,started,release]=` + string(paths) + `;
let text='';for await(const chunk of process.stdin)text+=chunk;const req=JSON.parse(text);
let installed=JSON.parse(await readFile(state,'utf8')),result=installed;
if(req.method==='install'){
 await writeFile(started,'');
 while(true){try{await access(release);break}catch{await new Promise(resolve=>setTimeout(resolve,10))}}
 result={name:req.name,version:'0.1.0',api:'1',present:true,source:req.source};
 installed.push(result);await writeFile(state,JSON.stringify(installed));
}
process.stdout.write(JSON.stringify({result}));`
	if err := os.WriteFile(main, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(t.Context(), ManagerConfig{Directory: directory, Runtime: c, PackageMain: main})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	s := plugin.RuntimeSettings{Packages: map[string]plugin.RuntimePackage{"fixture": base}, Instances: []plugin.InstanceConfig{{ID: "fixture", Package: "fixture", Enabled: true}}}
	if _, err := m.Apply(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	previous := m.runtime
	s.Packages["extra"] = plugin.RuntimePackage{Source: plugin.Source{Type: "directory", Spec: root}}
	s.Instances = append(s.Instances, plugin.InstanceConfig{ID: "extra", Package: "extra"})
	done := make(chan error, 1)
	go func() { _, err := m.Apply(t.Context(), s); done <- err }()
	defer func() { _ = os.WriteFile(release, nil, 0600) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("package installation did not start")
		case <-tick.C:
		}
	}
	status, err := m.Status(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, row := range status.Instances {
		states[row.Package] = row.State
	}
	if states["extra"] != "installing" || states["fixture"] != "ready" {
		t.Fatalf("wrong progress while sibling stays active: %+v", status.Instances)
	}
	tools, err := m.Tools(t.Context(), "session")
	if err != nil || len(tools) != 1 || tools[0].Spec().Name != "fixture__echo" {
		t.Fatalf("existing tools unavailable during download: %v %v", tools, err)
	}
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("package installation did not finish")
	}
	if previous != m.runtime || !previous.Alive() {
		t.Fatal("adding a disabled package restarted unrelated working plugins")
	}
}
