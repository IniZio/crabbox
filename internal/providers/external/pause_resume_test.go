package external

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

type scriptRunner struct{}

func (scriptRunner) Run(ctx context.Context, req core.LocalCommandRequest) (core.LocalCommandResult, error) {
	cmd := exec.CommandContext(ctx, req.Name, req.Args...)
	cmd.Stdin = req.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := core.LocalCommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if exit, ok := err.(*exec.ExitError); ok {
		res.ExitCode = exit.ExitCode()
	}
	return res, err
}

const pauseResumeResolveReply = `{"protocolVersion":1,"lease":{"leaseId":"cbx_abcdef123456","slug":"fast-coral","name":"box-1","cloudId":"provider/resource-9","status":"stopped"}}`

// pauseResumeBackend builds a backend whose adapter is a shell script that
// logs every request to a file and answers per operation.
func pauseResumeBackend(t *testing.T, optIn bool, pauseBody string) (*leaseBackend, string, *bytes.Buffer) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	isolateCrabboxState(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "requests.log")
	script := filepath.Join(dir, "adapter.sh")
	body := "#!/bin/sh\nreq=$(cat)\nprintf '%s\\n' \"$req\" >> " + logPath + "\n" +
		"case \"$req\" in\n" +
		"*'\"operation\":\"resolve\"'*) printf '%s' '" + pauseResumeResolveReply + "' ;;\n" +
		"*) " + pauseBody + " ;;\n" +
		"esac\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.External.Command = script
	cfg.External.Args = nil
	cfg.External.Capabilities.PauseResume = optIn
	var stderr bytes.Buffer
	return &leaseBackend{cfg: cfg, rt: core.Runtime{Stderr: &stderr, Exec: scriptRunner{}}}, logPath, &stderr
}

func readRequests(t *testing.T, path string) []protocolRequest {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []protocolRequest
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var req protocolRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.Fatalf("request %q: %v", line, err)
		}
		out = append(out, req)
	}
	return out
}

func TestPauseResumeSendResolvedLeaseIdentity(t *testing.T) {
	backend, logPath, _ := pauseResumeBackend(t, true, `printf '%s' '{"protocolVersion":1}'`)
	if err := backend.Pause(context.Background(), core.PauseRequest{ID: "fast-coral"}); err != nil {
		t.Fatal(err)
	}
	if err := backend.Resume(context.Background(), core.ResumeRequest{ID: "fast-coral"}); err != nil {
		t.Fatal(err)
	}
	var ops []protocolRequest
	for _, req := range readRequests(t, logPath) {
		if req.Operation == "pause" || req.Operation == "resume" {
			ops = append(ops, req)
		}
	}
	if len(ops) != 2 || ops[0].Operation != "pause" || ops[1].Operation != "resume" {
		t.Fatalf("ops=%#v", ops)
	}
	for _, req := range ops {
		lease := req.Lease
		if lease == nil || lease.LeaseID != "cbx_abcdef123456" || lease.Slug != "fast-coral" || lease.Name != "box-1" || lease.CloudID != "provider/resource-9" {
			t.Fatalf("%s lease=%#v", req.Operation, lease)
		}
	}
}

func TestPauseResumeRequireOptIn(t *testing.T) {
	backend, logPath, _ := pauseResumeBackend(t, false, `printf '%s' '{"protocolVersion":1}'`)
	for _, op := range []string{"pause", "resume"} {
		var err error
		if op == "pause" {
			err = backend.Pause(context.Background(), core.PauseRequest{ID: "fast-coral"})
		} else {
			err = backend.Resume(context.Background(), core.ResumeRequest{ID: "fast-coral"})
		}
		var exit core.ExitError
		if !core.AsExitError(err, &exit) || exit.Code != 2 || !strings.Contains(exit.Message, "provider=external") || !strings.Contains(exit.Message, op) {
			t.Fatalf("%s err=%v", op, err)
		}
	}
	if reqs := readRequests(t, logPath); len(reqs) != 0 {
		t.Fatalf("adapter invoked without opt-in: %#v", reqs)
	}
}

func TestPauseResumeAdapterFailuresCarryStderr(t *testing.T) {
	t.Run("error response", func(t *testing.T) {
		backend, _, _ := pauseResumeBackend(t, true, `echo "api said: instance busy" >&2; printf '%s' '{"protocolVersion":1,"error":"boom"}'`)
		err := backend.Pause(context.Background(), core.PauseRequest{ID: "fast-coral"})
		var exit core.ExitError
		if !core.AsExitError(err, &exit) || exit.Code == 0 || !strings.Contains(exit.Message, "boom") || !strings.Contains(exit.Message, "api said: instance busy") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("nonzero exit", func(t *testing.T) {
		backend, _, _ := pauseResumeBackend(t, true, `echo "disk quota hit" >&2; exit 7`)
		err := backend.Resume(context.Background(), core.ResumeRequest{ID: "fast-coral"})
		var exit core.ExitError
		if !core.AsExitError(err, &exit) || exit.Code != 7 || !strings.Contains(exit.Message, "disk quota hit") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("long stderr keeps tail", func(t *testing.T) {
		backend, _, _ := pauseResumeBackend(t, true, `i=0; while [ $i -lt 400 ]; do echo "line-$i-padding" >&2; i=$((i+1)); done; printf '%s' '{"protocolVersion":1,"error":"boom"}'`)
		err := backend.Pause(context.Background(), core.PauseRequest{ID: "fast-coral"})
		if err == nil || !strings.Contains(err.Error(), "line-399-padding") || strings.Contains(err.Error(), "line-0-padding") {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestPauseResumeErrorResponseStderrOnlyForPauseResume(t *testing.T) {
	runner := &recordingRunner{stdout: `{"protocolVersion":1,"error":"boom"}`}
	backend := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: io.Discard, Exec: runner}}
	if _, err := backend.invoke(context.Background(), protocolRequest{Operation: "doctor"}); err == nil || strings.Contains(err.Error(), "boom:") {
		t.Fatalf("err=%v", err)
	}
}

func TestPauseResumeCapabilityDoesNotChangeClaimScope(t *testing.T) {
	cfg := testConfig()
	cfg.External.Capabilities.IdempotentLeaseID = true
	before, err := externalControllerScope(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.External.Capabilities.PauseResume = true
	after, err := externalControllerScope(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("scope changed: %s != %s", before, after)
	}
}
