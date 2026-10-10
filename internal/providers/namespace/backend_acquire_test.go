package namespace

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

// acquireFailRunner fails any call whose args start with a listed verb.
type acquireFailRunner struct {
	calls []string
	fail  map[string]bool
}

func (r *acquireFailRunner) Run(_ context.Context, req core.LocalCommandRequest) (core.LocalCommandResult, error) {
	r.calls = append(r.calls, req.Name+" "+strings.Join(req.Args, " "))
	if len(req.Args) > 0 && r.fail[req.Args[0]] {
		return core.LocalCommandResult{ExitCode: 2}, errors.New("boom " + req.Args[0])
	}
	return core.LocalCommandResult{}, nil
}

func acquireWithFailures(t *testing.T, keep bool, fail map[string]bool) (*acquireFailRunner, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	runner := &acquireFailRunner{fail: fail}
	backend := &namespaceLeaseBackend{
		cfg: core.Config{Namespace: core.NamespaceConfig{WorkRoot: "/workspaces/crabbox"}},
		rt:  core.Runtime{Stdout: io.Discard, Stderr: io.Discard, Exec: runner},
	}
	_, err := backend.Acquire(context.Background(), core.AcquireRequest{Keep: keep, Repo: core.Repo{Root: t.TempDir()}})
	return runner, err
}

func countPrefix(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func TestAcquirePrepareFailureDeletesDevboxEvenWithKeep(t *testing.T) {
	runner, err := acquireWithFailures(t, true, map[string]bool{"prepare": true, "ssh-config": true, "configure-ssh": true})
	if err == nil {
		t.Fatal("want prepare error")
	}
	if countPrefix(runner.calls, "devbox delete ") != 1 {
		t.Fatalf("want one delete call, calls=%#v", runner.calls)
	}
	if strings.Contains(err.Error(), "delete") {
		t.Fatalf("original error masked: %v", err)
	}
}

func TestAcquirePrepareFailureNotMaskedByDeleteFailure(t *testing.T) {
	runner, err := acquireWithFailures(t, true, map[string]bool{"prepare": true, "ssh-config": true, "configure-ssh": true, "delete": true, "destroy": true})
	if err == nil {
		t.Fatal("want prepare error")
	}
	if countPrefix(runner.calls, "devbox delete ") != 1 || countPrefix(runner.calls, "devbox destroy ") != 1 {
		t.Fatalf("calls=%#v", runner.calls)
	}
	if strings.Contains(err.Error(), "delete failed") {
		t.Fatalf("delete failure masked original error: %v", err)
	}
}

type specCaptureRunner struct{ spec string }

func (r *specCaptureRunner) Run(_ context.Context, req core.LocalCommandRequest) (core.LocalCommandResult, error) {
	for i, a := range req.Args {
		if a == "--from" && i+1 < len(req.Args) {
			b, err := os.ReadFile(req.Args[i+1])
			if err != nil {
				return core.LocalCommandResult{}, err
			}
			r.spec = string(b)
		}
	}
	return core.LocalCommandResult{}, nil
}

func TestCreateDevboxEgressDomains(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{" , ", nil},
		{"a.com, *.b.com ,", []string{"a.com", "*.b.com"}},
	} {
		runner := &specCaptureRunner{}
		backend := &namespaceLeaseBackend{
			cfg: core.Config{Namespace: core.NamespaceConfig{EgressDomains: tc.raw}},
			rt:  core.Runtime{Stdout: io.Discard, Stderr: io.Discard, Exec: runner},
		}
		spec := namespaceCreateSpec{Name: "n", Image: "i", Size: "m", NetworkPolicy: namespaceNetworkPolicy(backend.cfg)}
		if err := backend.createDevbox(context.Background(), spec); err != nil {
			t.Fatal(err)
		}
		if tc.want == nil {
			if strings.Contains(runner.spec, "network_policy") {
				t.Fatalf("raw=%q unexpected policy: %s", tc.raw, runner.spec)
			}
			continue
		}
		for _, d := range tc.want {
			if !strings.Contains(runner.spec, "network_policy:") || !strings.Contains(runner.spec, "- "+d) && !strings.Contains(runner.spec, "- '"+d+"'") && !strings.Contains(runner.spec, "- \""+d+"\"") {
				t.Fatalf("raw=%q missing %s: %s", tc.raw, d, runner.spec)
			}
		}
	}
}
