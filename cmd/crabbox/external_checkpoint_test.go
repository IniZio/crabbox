//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crabbox/internal/cli"
)

const externalCheckpointAdapter = `import json, os, sys
state_path, log_path = sys.argv[1], sys.argv[2]
req = json.load(sys.stdin)
with open(log_path, "a") as log:
    log.write(json.dumps(req) + "\n")
state = {"leases": {}, "checkpoints": {}}
if os.path.exists(state_path):
    with open(state_path) as f:
        state = json.load(f)
op = req["operation"]

def lease_for(desired):
    return {"leaseId": desired["leaseId"], "slug": desired["slug"], "name": desired["name"],
            "cloudId": "box-" + desired["leaseId"], "status": "ready",
            "ssh": {"user": os.environ["USER"], "host": "127.0.0.1", "port": "22"}}

def find(identifier):
    for lease in state["leases"].values():
        if identifier in (lease["leaseId"], lease["slug"], lease["name"], lease["cloudId"]):
            return lease
    return None

reply = {}
if op in ("acquire", "checkpointFork"):
    if op == "checkpointFork" and req["checkpointId"] not in state["checkpoints"]:
        sys.stderr.write("checkpoint not found: " + req["checkpointId"] + "\n")
        sys.exit(1)
    lease = lease_for(req["desired"])
    state["leases"][lease["leaseId"]] = lease
    reply = {"lease": lease}
elif op in ("resolve", "touch"):
    identifier = req.get("id") or (req.get("lease") or {}).get("leaseId", "")
    lease = find(identifier)
    if lease is None:
        reply = {"error": identifier + " not found"}
    else:
        reply = {"lease": lease}
elif op == "list":
    reply = {"leases": list(state["leases"].values())}
elif op == "release":
    state["leases"].pop((req.get("lease") or {}).get("leaseId", ""), None)
elif op == "checkpointCreate":
    if find(req["leaseId"]) is None:
        reply = {"error": "lease " + req["leaseId"] + " not found"}
    else:
        state["checkpoints"][req["checkpointId"]] = {"leaseId": req["leaseId"], "name": req.get("name", "")}
        reply = {"checkpointId": req["checkpointId"], "bytes": 4096}
elif op == "checkpointDelete":
    state["checkpoints"].pop(req["checkpointId"], None)
reply["protocolVersion"] = 1
with open(state_path, "w") as f:
    json.dump(state, f)
print(json.dumps(reply))
`

type externalCheckpointFixture struct {
	dir     string
	config  string
	logPath string
	state   string
}

func newExternalCheckpointFixture(t *testing.T, checkpoints bool) externalCheckpointFixture {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	for _, tool := range []string{"bash", "git", "rsync", "tar"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available for the SSH ready check", tool)
		}
	}
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".state"))
	clearCrabboxDoctorEnv(t)
	t.Setenv("CRABBOX_WORK_ROOT", "")
	if os.Getenv("USER") == "" {
		t.Setenv("USER", "tester")
	}

	tools := filepath.Join(home, "tools")
	if err := os.MkdirAll(tools, 0o700); err != nil {
		t.Fatal(err)
	}
	// Every SSH command runs locally, so the guest is this test's temp tree.
	if err := os.WriteFile(filepath.Join(tools, "ssh"), []byte("#!/bin/sh\nfor arg; do remote=\"$arg\"; done\nexec /bin/sh -c \"$remote\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))

	repo := filepath.Join(home, "my-app")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	t.Chdir(repo)

	f := externalCheckpointFixture{
		dir:     home,
		config:  filepath.Join(home, "config.yaml"),
		logPath: filepath.Join(home, "requests.log"),
		state:   filepath.Join(home, "adapter-state.json"),
	}
	adapter := filepath.Join(home, "adapter.py")
	if err := os.WriteFile(adapter, []byte(externalCheckpointAdapter), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRABBOX_CONFIG", f.config)
	f.writeConfig(t, python, adapter, checkpoints)
	return f
}

func (f externalCheckpointFixture) writeConfig(t *testing.T, python, adapter string, checkpoints bool) {
	t.Helper()
	config := "provider: external\n" +
		"external:\n" +
		"  command: " + python + "\n" +
		"  args: [" + adapter + ", " + f.state + ", " + f.logPath + "]\n" +
		"  workRoot: " + filepath.Join(f.dir, "work") + "\n" +
		"  connection:\n    ssh:\n      trustProviderOutput: true\n"
	if checkpoints {
		config += "  capabilities:\n    checkpoints: true\n"
	}
	if err := os.WriteFile(f.config, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f externalCheckpointFixture) operations(t *testing.T, operation string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(f.logPath)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var req map[string]any
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.Fatalf("request %q: %v", line, err)
		}
		if req["operation"] == operation {
			out = append(out, req)
		}
	}
	return out
}

func (f externalCheckpointFixture) forgetCheckpoint(t *testing.T, id string) {
	t.Helper()
	data, err := os.ReadFile(f.state)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	delete(state["checkpoints"], id)
	data, err = json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.state, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runExternalCheckpointCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var stdout, stderr bytes.Buffer
	err := (cli.App{Stdout: &stdout, Stderr: &stderr}).Run(ctx, args)
	return stdout.String(), stderr.String(), err
}

func mustRunExternalCheckpointCLI(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, err := runExternalCheckpointCLI(t, args...)
	if err != nil {
		t.Fatalf("crabbox %s: %v\nstdout=%s\nstderr=%s", strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout
}

type externalCheckpointRecord struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Provider   string `json:"provider"`
	LeaseID    string `json:"leaseId"`
	CreatedAt  string `json:"createdAt"`
	LastUsedAt string `json:"lastUsedAt"`
	Native     struct {
		ImageID string `json:"imageId"`
		State   string `json:"state"`
		Bytes   int64  `json:"bytes"`
	} `json:"native"`
}

func TestExternalCheckpointCreateListForkDelete(t *testing.T) {
	f := newExternalCheckpointFixture(t, true)
	mustRunExternalCheckpointCLI(t, "warmup", "--slug", "base", "--keep")
	acquired := f.operations(t, "acquire")
	if len(acquired) != 1 {
		t.Fatalf("acquire requests=%v", acquired)
	}
	source := acquired[0]["desired"].(map[string]any)["leaseId"].(string)

	var created externalCheckpointRecord
	out := mustRunExternalCheckpointCLI(t, "checkpoint", "create", "--id", source, "--name", "after-clone", "--mode", "native", "--json")
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("create JSON %q: %v", out, err)
	}
	if !strings.HasPrefix(created.ID, "chk_") || created.Name != "after-clone" || created.Kind != cli.CheckpointKindExternal || created.Provider != "external" ||
		created.LeaseID != source || created.Native.ImageID != created.ID || created.Native.State != "available" || created.Native.Bytes != 4096 || created.CreatedAt == "" {
		t.Fatalf("created=%+v", created)
	}
	creates := f.operations(t, "checkpointCreate")
	if len(creates) != 1 || creates[0]["leaseId"] != source || creates[0]["checkpointId"] != created.ID || creates[0]["name"] != "after-clone" {
		t.Fatalf("checkpointCreate requests=%v", creates)
	}

	var listed []externalCheckpointRecord
	out = mustRunExternalCheckpointCLI(t, "checkpoint", "list", "--json")
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("list JSON %q: %v", out, err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID || listed[0].Name != "after-clone" || listed[0].Native.Bytes != 4096 || listed[0].LastUsedAt == "" {
		t.Fatalf("listed=%+v", listed)
	}

	var forked struct {
		CheckpointID string `json:"checkpointId"`
		LeaseID      string `json:"leaseId"`
		Slug         string `json:"slug"`
		Provider     string `json:"provider"`
	}
	out = mustRunExternalCheckpointCLI(t, "checkpoint", "fork", created.ID, "--slug", "retry-1", "--json")
	if err := json.Unmarshal([]byte(out), &forked); err != nil {
		t.Fatalf("fork JSON %q: %v", out, err)
	}
	if forked.CheckpointID != created.ID || forked.LeaseID == source || !strings.HasPrefix(forked.LeaseID, "cbx_") || forked.Slug != "retry-1" || forked.Provider != "external" {
		t.Fatalf("forked=%+v", forked)
	}
	forks := f.operations(t, "checkpointFork")
	if len(forks) != 1 || forks[0]["checkpointId"] != created.ID || forks[0]["desired"].(map[string]any)["leaseId"] != forked.LeaseID {
		t.Fatalf("checkpointFork requests=%v", forks)
	}

	mustRunExternalCheckpointCLI(t, "checkpoint", "delete", created.ID)
	deletes := f.operations(t, "checkpointDelete")
	if len(deletes) != 1 || deletes[0]["checkpointId"] != created.ID {
		t.Fatalf("checkpointDelete requests=%v", deletes)
	}
	if out := mustRunExternalCheckpointCLI(t, "checkpoint", "list", "--json"); strings.TrimSpace(out) != "[]" {
		t.Fatalf("list after delete=%s", out)
	}
}

func TestExternalCheckpointForkOfUnknownCheckpointFails(t *testing.T) {
	f := newExternalCheckpointFixture(t, true)
	mustRunExternalCheckpointCLI(t, "warmup", "--slug", "base", "--keep")
	var created externalCheckpointRecord
	out := mustRunExternalCheckpointCLI(t, "checkpoint", "create", "--id", "base", "--mode", "native", "--json")
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatalf("create JSON %q: %v", out, err)
	}
	f.forgetCheckpoint(t, created.ID)
	_, _, err := runExternalCheckpointCLI(t, "checkpoint", "fork", created.ID, "--json")
	var exit cli.ExitError
	if !cli.AsExitError(err, &exit) || exit.Code == 0 || !strings.Contains(err.Error(), "checkpoint not found") {
		t.Fatalf("fork of a forgotten checkpoint err=%v", err)
	}
}

func TestExternalCheckpointCreateWithoutCapabilityFails(t *testing.T) {
	f := newExternalCheckpointFixture(t, false)
	mustRunExternalCheckpointCLI(t, "warmup", "--slug", "base", "--keep")
	_, _, err := runExternalCheckpointCLI(t, "checkpoint", "create", "--id", "base", "--mode", "native", "--json")
	if err == nil || !strings.Contains(err.Error(), "provider=external does not support checkpoints") {
		t.Fatalf("create without capability err=%v", err)
	}
	if creates := f.operations(t, "checkpointCreate"); len(creates) != 0 {
		t.Fatalf("adapter received checkpointCreate without the capability: %v", creates)
	}
	if out := mustRunExternalCheckpointCLI(t, "checkpoint", "list", "--json"); strings.TrimSpace(out) != "[]" {
		t.Fatalf("failed create left a record: %s", out)
	}
}

func TestExternalCheckpointProvidersReportCapability(t *testing.T) {
	newExternalCheckpointFixture(t, true)
	out := mustRunExternalCheckpointCLI(t, "providers", "--json", "--configured", "--feature", "workspace-fork")
	var entries []struct {
		Provider string   `json:"provider"`
		Features []string `json:"features"`
	}
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("providers JSON %q: %v", out, err)
	}
	for _, entry := range entries {
		if entry.Provider == "external" {
			joined := strings.Join(entry.Features, ",")
			for _, feature := range []string{"workspace-checkpoint", "workspace-fork", "provider-snapshot"} {
				if !strings.Contains(joined, feature) {
					t.Fatalf("external features=%v, want %s", entry.Features, feature)
				}
			}
			return
		}
	}
	t.Fatalf("external missing from configured providers with workspace-fork: %s", out)
}
