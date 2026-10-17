package external

import (
	"bytes"
	"context"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func acquireIdentityMismatch(t *testing.T, req core.AcquireRequest) *sequenceRunner {
	t.Helper()
	isolateCrabboxState(t)
	runner := &sequenceRunner{responses: []string{
		resolveStatusReply("ready"),
		`{"protocolVersion":1}`,
	}}
	var stderr bytes.Buffer
	backend := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: &stderr, Exec: runner}}
	if _, err := backend.Acquire(context.Background(), req); err == nil {
		t.Fatal("want identity mismatch")
	}
	return runner
}

func TestAcquireKeepReleasesLeaseOnPreRoutingFailure(t *testing.T) {
	runner := acquireIdentityMismatch(t, core.AcquireRequest{Keep: true})
	if len(runner.operations) != 2 || runner.operations[1] != "release" {
		t.Fatalf("operations=%v", runner.operations)
	}
}

func TestAcquireFixedKeptAttemptRetainsLeaseOnPreRoutingFailure(t *testing.T) {
	runner := acquireIdentityMismatch(t, core.AcquireRequest{Keep: true, RequestedLeaseID: "cbx_000000000002"})
	if len(runner.operations) != 1 {
		t.Fatalf("operations=%v", runner.operations)
	}
}
