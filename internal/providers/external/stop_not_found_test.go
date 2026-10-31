package external

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

type failingProtocolRunner struct {
	stdout string
	stderr string
	code   int
}

func (r failingProtocolRunner) Run(_ context.Context, req core.LocalCommandRequest) (core.LocalCommandResult, error) {
	_, _ = io.ReadAll(req.Stdin)
	result := core.LocalCommandResult{Stdout: r.stdout, Stderr: r.stderr, ExitCode: r.code}
	if r.code == 0 {
		return result, nil
	}
	return result, errors.New("exit status 1")
}

func stopResolveExit(t *testing.T, runner core.CommandRunner) (int, string) {
	t.Helper()
	isolateCrabboxState(t)
	backend := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: io.Discard, Exec: runner}}
	_, err := backend.Resolve(context.Background(), core.ResolveRequest{ID: "moor-99-abcdef", ReleaseOnly: true})
	if err == nil {
		t.Fatal("resolve of a missing lease succeeded")
	}
	return core.ExitCodeForError(err, -1), err.Error()
}

func TestStopResolveProviderNotFoundExitsFour(t *testing.T) {
	for name, runner := range map[string]failingProtocolRunner{
		"json error":     {stdout: `{"protocolVersion":1,"error":"instance vm1 not found"}`, stderr: "trace", code: 1},
		"stderr":         {stderr: "instance vm1 not found\n", code: 1},
		"exit zero json": {stdout: `{"protocolVersion":1,"error":"lease moor-99-abcdef not found"}`},
	} {
		t.Run(name, func(t *testing.T) {
			code, message := stopResolveExit(t, runner)
			if code != 4 || !strings.Contains(message, "not found") {
				t.Fatalf("exit=%d message=%q, want exit 4 with not found", code, message)
			}
		})
	}
}

func TestStopResolveOtherProviderFailureKeepsItsExit(t *testing.T) {
	code, message := stopResolveExit(t, failingProtocolRunner{
		stdout: `{"protocolVersion":1,"error":"resolve needs a cloud id or a desired lease"}`,
		code:   1,
	})
	if code != 1 || !strings.Contains(message, "resolve needs a cloud id") {
		t.Fatalf("exit=%d message=%q, want the provider's exit 1", code, message)
	}
}

func TestReleaseProviderNotFoundExitsFour(t *testing.T) {
	isolateCrabboxState(t)
	runner := failingProtocolRunner{stdout: `{"protocolVersion":1,"error":"instance vm1 not found"}`, code: 1}
	backend := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: io.Discard, Exec: runner}}
	lease := core.LeaseTarget{LeaseID: "cbx_000000000001", Server: core.Server{Provider: "external", CloudID: "vm1", Labels: map[string]string{"slug": "moor-99"}}}
	err := backend.ReleaseLease(context.Background(), core.ReleaseLeaseRequest{Lease: lease})
	if code := core.ExitCodeForError(err, -1); code != 4 {
		t.Fatalf("exit=%d err=%v, want exit 4", code, err)
	}
}

// Lifecycle commands get no stdin, unlike protocol calls.
type failingLifecycleRunner struct{ stderr string }

func (r failingLifecycleRunner) Run(context.Context, core.LocalCommandRequest) (core.LocalCommandResult, error) {
	return core.LocalCommandResult{Stderr: r.stderr, ExitCode: 1}, errors.New("exit status 1")
}

func lifecycleBackend(runner core.CommandRunner) *leaseBackend {
	cfg := testConfig()
	cfg.External.Command = ""
	cfg.External.Lifecycle.Acquire.Argv = []string{"devboxctl", "new", "{{name}}"}
	cfg.External.Lifecycle.Resolve.Argv = []string{"devboxctl", "show", "{{name}}"}
	cfg.External.Lifecycle.Resolve.Output = lifecycleOutputJSONLease
	cfg.External.Lifecycle.Release.Argv = []string{"devboxctl", "rm", "{{name}}"}
	return &leaseBackend{cfg: cfg, rt: core.Runtime{Stderr: io.Discard, Exec: runner}}
}

func lifecycleExit(t *testing.T, operation string, runner core.CommandRunner) (int, string) {
	t.Helper()
	isolateCrabboxState(t)
	backend := lifecycleBackend(runner)
	_, err := backend.invoke(context.Background(), protocolRequest{
		Operation: operation,
		Lease:     &protocolLease{LeaseID: "cbx_000000000001", CloudID: "vm1", Slug: "moor-99"},
		Desired:   &desiredLease{LeaseID: "cbx_000000000001", Slug: "moor-99", Name: "crabbox-moor-99"},
	})
	if err == nil {
		t.Fatalf("lifecycle %s of a missing lease succeeded", operation)
	}
	return core.ExitCodeForError(err, -1), err.Error()
}

func TestLifecycleNotFoundExitsFourForStopOperations(t *testing.T) {
	for _, operation := range []string{"resolve", "release"} {
		t.Run(operation, func(t *testing.T) {
			code, message := lifecycleExit(t, operation, failingLifecycleRunner{stderr: "lease moor-99 not found\n"})
			if code != 4 || !strings.Contains(message, "lease moor-99 not found") {
				t.Fatalf("exit=%d message=%q, want exit 4 keeping the message", code, message)
			}
		})
	}
}

func TestLifecycleResolveNotFoundReachesBackendAsExitFour(t *testing.T) {
	isolateCrabboxState(t)
	backend := lifecycleBackend(failingLifecycleRunner{stderr: "lease moor-99 not found"})
	_, err := backend.Resolve(context.Background(), core.ResolveRequest{ID: "moor-99-abcdef", ReleaseOnly: true})
	if code := core.ExitCodeForError(err, -1); code != 4 {
		t.Fatalf("exit=%d err=%v, want exit 4", code, err)
	}
}

func TestLifecycleOtherFailureKeepsItsExit(t *testing.T) {
	for _, operation := range []string{"resolve", "release"} {
		t.Run(operation, func(t *testing.T) {
			code, message := lifecycleExit(t, operation, failingLifecycleRunner{stderr: "permission denied\n"})
			if code != 1 || !strings.Contains(message, "permission denied") {
				t.Fatalf("exit=%d message=%q, want the command's exit 1", code, message)
			}
		})
	}
}

func TestLifecycleAcquireNotFoundKeepsItsExit(t *testing.T) {
	code, message := lifecycleExit(t, "acquire", failingLifecycleRunner{stderr: "image ubuntu not found\n"})
	if code != 1 || !strings.Contains(message, "image ubuntu not found") {
		t.Fatalf("exit=%d message=%q, want the command's exit 1", code, message)
	}
}
