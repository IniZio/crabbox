package external

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	core "github.com/openclaw/crabbox/internal/cli"
)

const claimedLeaseID = "cbx_00000000000c"

func claimWithRouting(t *testing.T, b *leaseBackend, slug string) {
	t.Helper()
	claimExternalLease(t, b.cfg, claimedLeaseID, slug, t.TempDir(), time.Minute, false)
	lease := core.ExternalRoutedLease{LeaseID: claimedLeaseID, Slug: slug, Name: core.LeaseProviderName(claimedLeaseID, slug)}
	if _, err := core.PersistValidatedExternalLeaseRouting(lease, b.cfg.External); err != nil {
		t.Fatal(err)
	}
}

func requireLocalStateGone(t *testing.T) {
	t.Helper()
	if _, present, err := core.ReadLeaseClaimWithPresence(claimedLeaseID); err != nil || present {
		t.Fatalf("claim of an absent lease survived the stop: present=%v err=%v", present, err)
	}
	path, err := core.ExternalRoutingPath(claimedLeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("routing of an absent lease survived the stop: %v", err)
	}
}

func releaseClaimed(b *leaseBackend) error {
	lease := core.LeaseTarget{LeaseID: claimedLeaseID, Server: core.Server{Provider: providerName, CloudID: "vm1", Labels: map[string]string{"slug": "moor-7"}}}
	return b.ReleaseLease(context.Background(), core.ReleaseLeaseRequest{Lease: lease})
}

func TestReleaseNotFoundOfClaimedLeaseClearsLocalState(t *testing.T) {
	isolateCrabboxState(t)
	runner := failingProtocolRunner{stdout: `{"protocolVersion":1,"error":"instance vm1 not found"}`, code: 1}
	b := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: io.Discard, Exec: runner}}
	claimWithRouting(t, b, "moor-7")
	if code := core.ExitCodeForError(releaseClaimed(b), -1); code != 4 {
		t.Fatalf("exit=%d, want 4", code)
	}
	requireLocalStateGone(t)
}

func TestReleaseOtherFailureKeepsClaimedLeaseState(t *testing.T) {
	isolateCrabboxState(t)
	runner := failingProtocolRunner{stdout: `{"protocolVersion":1,"error":"provider api unavailable"}`, code: 1}
	b := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: io.Discard, Exec: runner}}
	claimWithRouting(t, b, "moor-7")
	if err := releaseClaimed(b); core.ExitCodeForError(err, -1) == 4 {
		t.Fatalf("err=%v, a transient failure must not read as gone", err)
	}
	claim, err := core.ReadLeaseClaim(claimedLeaseID)
	if err != nil || claim.LeaseID == "" {
		t.Fatalf("claim lost after a failed release: %v", err)
	}
}

func TestLifecycleReleaseNotFoundOfClaimedLeaseClearsLocalState(t *testing.T) {
	isolateCrabboxState(t)
	b := lifecycleBackend(failingLifecycleRunner{stderr: "lease moor-7 not found\n"})
	claimWithRouting(t, b, "moor-7")
	if code := core.ExitCodeForError(releaseClaimed(b), -1); code != 4 {
		t.Fatalf("exit=%d, want 4", code)
	}
	requireLocalStateGone(t)
}

func TestStopResolveNotFoundOfClaimedLeaseClearsLocalState(t *testing.T) {
	isolateCrabboxState(t)
	runner := failingProtocolRunner{stdout: `{"protocolVersion":1,"error":"instance vm1 not found"}`, code: 1}
	b := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: io.Discard, Exec: runner}}
	claimWithRouting(t, b, "moor-7")
	_, err := b.Resolve(context.Background(), core.ResolveRequest{ID: "moor-7", ReleaseOnly: true})
	if code := core.ExitCodeForError(err, -1); code != 4 {
		t.Fatalf("exit=%d err=%v, want 4", code, err)
	}
	requireLocalStateGone(t)
}

func TestStopWithTwoClaimsForOneSlugExitsFive(t *testing.T) {
	isolateCrabboxState(t)
	cfg := testConfig()
	claimExternalLease(t, cfg, "cbx_00000000000a", "moor-7", t.TempDir(), time.Minute, false)
	claimExternalLease(t, cfg, "cbx_00000000000b", "moor-7", t.TempDir(), time.Minute, false)
	err := stopLease(&fakeCloud{vms: map[string]string{}}, "moor-7")
	if code := core.ExitCodeForError(err, -1); code != 5 || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("exit=%d err=%v, want exit 5 naming the ambiguity", code, err)
	}
}
