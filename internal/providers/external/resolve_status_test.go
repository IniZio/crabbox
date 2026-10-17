package external

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	core "github.com/openclaw/crabbox/internal/cli"
)

func resolveStatusReply(status string) string {
	field := ""
	if status != "" {
		field = `"status":"` + status + `",`
	}
	return `{"protocolVersion":1,"lease":{"leaseId":"cbx_000000000001","slug":"s","name":"n","cloudId":"vm1",` + field + `"ssh":{"user":"u","host":"127.0.0.1","port":"1","readyCheck":"true"}}}`
}

func resolveStatusBackend(reply string) (*leaseBackend, *bytes.Buffer) {
	var stderr bytes.Buffer
	runner := &sequenceRunner{responses: []string{reply}}
	return &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: &stderr, Exec: runner}}, &stderr
}

func TestResolveFailsFastForSuspendedStatus(t *testing.T) {
	for _, status := range []string{"stopped", "paused", "Released"} {
		t.Run(status, func(t *testing.T) { testResolveSuspended(t, status) })
	}
}

func testResolveSuspended(t *testing.T, status string) {
	isolateCrabboxState(t)
	backend, _ := resolveStatusBackend(resolveStatusReply(status))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, err := backend.Resolve(ctx, core.ResolveRequest{ID: "cbx_000000000001", NoLocalStateMutations: true})
	var exit core.ExitError
	if !core.AsExitError(err, &exit) || exit.Code != 4 || !strings.Contains(exit.Message, status) || !strings.Contains(exit.Message, "crabbox stop --id cbx_000000000001") {
		t.Fatalf("err=%v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("resolve took %s", elapsed)
	}
}

func TestResolveOtherStatusesStillWaitForSSH(t *testing.T) {
	for _, status := range []string{"", "starting"} {
		t.Run("status="+status, func(t *testing.T) { testResolveWaitsForSSH(t, status) })
	}
}

func testResolveWaitsForSSH(t *testing.T, status string) {
	isolateCrabboxState(t)
	backend, _ := resolveStatusBackend(resolveStatusReply(status))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := backend.Resolve(ctx, core.ResolveRequest{ID: "cbx_000000000001", NoLocalStateMutations: true})
	if err == nil || strings.Contains(err.Error(), "resume or stop it") {
		t.Fatalf("want SSH wait failure, got %v", err)
	}
}

func TestResolveReleaseOnlyAcceptsStoppedStatus(t *testing.T) {
	isolateCrabboxState(t)
	backend, _ := resolveStatusBackend(resolveStatusReply("stopped"))
	lease, err := backend.Resolve(context.Background(), core.ResolveRequest{ID: "cbx_000000000001", ReleaseOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if lease.LeaseID != "cbx_000000000001" {
		t.Fatalf("lease=%#v", lease)
	}
}

func TestResolveStatusOnlySkipsSuspendedCheck(t *testing.T) {
	isolateCrabboxState(t)
	backend, _ := resolveStatusBackend(resolveStatusReply("stopped"))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := backend.Resolve(ctx, core.ResolveRequest{ID: "cbx_000000000001", StatusOnly: true, NoLocalStateMutations: true})
	if err == nil || strings.Contains(err.Error(), "resume or stop it") {
		t.Fatalf("want SSH wait failure, got %v", err)
	}
}
