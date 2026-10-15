package external

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

// firstStdinRunner keeps the acquire request; a failed acquire is followed by
// a rollback release that would overwrite recordingRunner.stdin.
type firstStdinRunner struct {
	recordingRunner
	first []byte
}

func (r *firstStdinRunner) Run(ctx context.Context, req core.LocalCommandRequest) (core.LocalCommandResult, error) {
	res, err := r.recordingRunner.Run(ctx, req)
	if r.first == nil {
		r.first = r.stdin
	}
	return res, err
}

func acquireStdin(t *testing.T, cfg core.Config, class string) []byte {
	t.Helper()
	isolateCrabboxState(t)
	runner := &firstStdinRunner{recordingRunner: recordingRunner{stdout: `{"protocolVersion":1,"lease":{"name":"created-without-ssh"}}`}}
	backend := &leaseBackend{cfg: cfg, rt: core.Runtime{Stderr: io.Discard, Exec: runner}}
	req := core.AcquireRequest{Options: core.LeaseOptions{Class: class}}
	_, _ = backend.Acquire(context.Background(), req)
	if len(runner.first) == 0 {
		t.Fatal("no request recorded")
	}
	return runner.first
}

func TestAcquireSendsExplicitClass(t *testing.T) {
	cfg := testConfig()
	cfg.Class = "fast"
	core.MarkClassExplicit(&cfg)
	stdin := acquireStdin(t, cfg, "fast")
	var got struct {
		Desired struct {
			Class string `json:"class"`
		} `json:"desired"`
	}
	if err := json.Unmarshal(stdin, &got); err != nil {
		t.Fatal(err)
	}
	if got.Desired.Class != "fast" {
		t.Fatalf("desired.class=%q stdin=%s", got.Desired.Class, stdin)
	}
}

func TestAcquireOmitsUnsetClass(t *testing.T) {
	cfg := testConfig()
	stdin := acquireStdin(t, cfg, cfg.Class)
	if strings.Contains(string(stdin), `"class"`) {
		t.Fatalf("stdin has class key: %s", stdin)
	}
}
