package external

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

const unclaimedSSH = `"ssh":{"user":"u","host":"127.0.0.1","port":"1","readyCheck":"true"}`

func acquireWithoutClaim(t *testing.T, cfg core.Config) (*sequenceRunner, desiredLease) {
	t.Helper()
	runner := &sequenceRunner{responses: []string{`{"protocolVersion":1,"lease":{"cloudId":"vm1",` + unclaimedSSH + `}}`}}
	backend := &leaseBackend{cfg: cfg, rt: core.Runtime{Stderr: io.Discard, Exec: runner}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Acquire(ctx, core.AcquireRequest{Keep: true, RequestedSlug: "moor-7"}); err == nil {
		t.Fatal("want readiness failure")
	}
	if len(runner.requests) != 1 || runner.requests[0].Desired == nil {
		t.Fatalf("acquire requests=%v", runner.operations)
	}
	if claim, err := core.ReadLeaseClaim(runner.requests[0].Desired.LeaseID); err != nil || claim.LeaseID != "" {
		t.Fatal("acquire wrote a claim before readiness")
	}
	return runner, *runner.requests[0].Desired
}

func TestStopDeletesLeaseWhoseAcquireFailedBeforeClaim(t *testing.T) {
	for _, by := range []string{"slug", "lease id", "name"} {
		t.Run(by, func(t *testing.T) {
			isolateCrabboxState(t)
			runner, acquired := acquireWithoutClaim(t, testConfig())
			id := map[string]string{"slug": acquired.Slug, "lease id": acquired.LeaseID, "name": acquired.Name}[by]
			runner.responses = []string{
				fmt.Sprintf(`{"protocolVersion":1,"lease":{"leaseId":%q,"slug":%q,"name":%q,"cloudId":"vm1",%s}}`, acquired.LeaseID, acquired.Slug, acquired.Name, unclaimedSSH),
				`{"protocolVersion":1}`,
			}
			backend := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: io.Discard, Exec: runner}}
			lease, err := backend.Resolve(context.Background(), core.ResolveRequest{ID: id, ReleaseOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			resolve := runner.requests[1]
			if resolve.Operation != "resolve" || resolve.Desired == nil || !reflect.DeepEqual(*resolve.Desired, acquired) {
				t.Fatalf("resolve desired=%+v, want %+v", resolve.Desired, acquired)
			}
			if err := backend.ReleaseLease(context.Background(), core.ReleaseLeaseRequest{Lease: lease}); err != nil {
				t.Fatal(err)
			}
			if release := runner.requests[2]; release.Operation != "release" || release.Lease.LeaseID != acquired.LeaseID || release.Lease.CloudID != "vm1" {
				t.Fatalf("release=%+v", release.Lease)
			}
			path, _ := core.ExternalRoutingPath(acquired.LeaseID)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("routing survived the release: %v", err)
			}
		})
	}
}

func TestUnclaimedRoutingFromAnotherProviderScopeIsIgnored(t *testing.T) {
	isolateCrabboxState(t)
	_, acquired := acquireWithoutClaim(t, testConfig())
	other := testConfig()
	other.External.Config = map[string]any{"namespace": "prod"}
	runner := &sequenceRunner{responses: []string{`{"protocolVersion":1,"error":"not found"}`}}
	backend := &leaseBackend{cfg: other, rt: core.Runtime{Stderr: io.Discard, Exec: runner}}
	_, _ = backend.Resolve(context.Background(), core.ResolveRequest{ID: acquired.Slug, ReleaseOnly: true})
	if len(runner.requests) != 1 || runner.requests[0].Desired != nil {
		t.Fatalf("resolve sent desired=%+v from another scope", runner.requests[0].Desired)
	}
}
