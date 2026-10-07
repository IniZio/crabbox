package azuresandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestSandboxBootstrapScriptAndPrivateEnvironment(t *testing.T) {
	f := &fixture{}
	b, acquire := fixtureBackend(t, f)
	if _, err := b.acquire(t.Context(), acquire); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	b.rt.Stdout, b.rt.Stderr = &stdout, &stderr
	directory := t.TempDir()
	b.api.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/executeShellCommand") {
			return f.request(t, r)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		// Exercise shell quoting against a real process, with only the fake host's
		// workspace path redirected to this test's isolated directory.
		command := strings.ReplaceAll(body["command"], "/workspace", directory)
		cmd := exec.CommandContext(r.Context(), "bash", "-c", command)
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		output, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if e, ok := err.(*exec.ExitError); ok {
				code = e.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		data, _ := json.Marshal(map[string]any{"exitCode": code, "stdout": string(output), "stderr": ""})
		return response(200, string(data)), nil
	})
	secret := "synthetic ' $(false) ; credential"
	result, err := b.Run(t.Context(), core.RunRequest{ID: acquire.RequestedLeaseID, Repo: acquire.Repo, Keep: true, NoSync: true,
		Env: map[string]string{"BOOTSTRAP_TOKEN": secret}, ScriptRequested: true,
		Script: &core.RunScriptSpec{Source: "stdin", Data: []byte("test \"$BOOTSTRAP_TOKEN\" = \"$1\" && printf bootstrap-ok")}, Command: []string{secret}})
	if err != nil || result.ExitCode != 0 || stdout.String() != "bootstrap-ok" {
		t.Fatalf("bootstrap: %+v %v stdout=%q", result, err, stdout.String())
	}
	if strings.Contains(stderr.String(), secret) {
		t.Fatal("credential leaked to diagnostics")
	}
}

func TestSandboxUnknownCommandRetainsLease(t *testing.T) {
	f := &fixture{}
	b, acquire := fixtureBackend(t, f)
	if _, err := b.acquire(t.Context(), acquire); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	b.api.http.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(r.URL.Path, "/executeShellCommand") {
			return f.request(t, r)
		}
		cancel()
		return nil, context.Canceled
	})
	_, err := b.Run(ctx, core.RunRequest{ID: acquire.RequestedLeaseID, Repo: acquire.Repo, Keep: true, NoSync: true, Command: []string{"true"}})
	if err == nil {
		t.Fatal("unknown command outcome reported success")
	}
	claim, err := b.claim(acquire.RequestedLeaseID)
	if err != nil || claim.FixedCreateIntent.State != "acquired" || f.deletes != 0 {
		t.Fatal("uncertain execution released ownership")
	}
}

func TestSandboxExpiredAllocationCannotExecuteOrRenew(t *testing.T) {
	f := &fixture{}
	b, acquire := fixtureBackend(t, f)
	if _, err := b.acquire(t.Context(), acquire); err != nil {
		t.Fatal(err)
	}
	claim, err := b.claim(acquire.RequestedLeaseID)
	if err != nil {
		t.Fatal(err)
	}
	claim.FixedCreateIntent.Attempt["expires_at"] = time.Now().Add(-time.Second).Format(time.RFC3339Nano)
	if _, err := executionDeadline(claim); err == nil {
		t.Fatal("expired allocation admitted")
	}
	claim.FixedCreateIntent.State = "released"
	claim.FixedCreateIntent.Attempt["expires_at"] = time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	if _, err := executionDeadline(claim); err == nil {
		t.Fatal("released allocation admitted")
	}
}

var _ core.DelegatedRunBackend = (*backend)(nil)
var _ core.DelegatedFixedWarmupBackend = (*backend)(nil)
var _ core.LeaseHeartbeatBackend = (*backend)(nil)
