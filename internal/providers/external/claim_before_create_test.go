package external

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

// fakeCloud is a provider that, like agent37, can only find a VM with no cloud
// id by the desired lease name.
type fakeCloud struct {
	vms map[string]string
}

func (c *fakeCloud) Run(_ context.Context, req core.LocalCommandRequest) (core.LocalCommandResult, error) {
	var request protocolRequest
	if err := json.NewDecoder(req.Stdin).Decode(&request); err != nil {
		return core.LocalCommandResult{}, err
	}
	reply := func(body string) (core.LocalCommandResult, error) {
		return core.LocalCommandResult{Stdout: `{"protocolVersion":1` + body + `}`}, nil
	}
	switch request.Operation {
	case "acquire":
		c.vms[request.Desired.Name] = "vm-" + request.Desired.Name
		return core.LocalCommandResult{Stderr: "killed", ExitCode: 137}, errors.New("signal: killed")
	case "resolve":
		if request.Desired == nil {
			return reply(`,"error":"no cloud id or lease name to look up; lease not found"`)
		}
		d := request.Desired
		id, ok := c.vms[d.Name]
		if !ok {
			return reply(fmt.Sprintf(`,"error":"instance %s not found"`, d.Name))
		}
		return reply(fmt.Sprintf(`,"lease":{"leaseId":%q,"slug":%q,"name":%q,"cloudId":%q,%s}`, d.LeaseID, d.Slug, d.Name, id, unclaimedSSH))
	case "release":
		for name, id := range c.vms {
			if id == request.Lease.CloudID {
				delete(c.vms, name)
				return reply("")
			}
		}
		return reply(`,"error":"instance not found"`)
	}
	return reply(`,"error":"unexpected operation"`)
}

func stopLease(cloud *fakeCloud, id string) error {
	backend := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: io.Discard, Exec: cloud}}
	lease, err := backend.Resolve(context.Background(), core.ResolveRequest{ID: id, ReleaseOnly: true})
	if err != nil {
		return err
	}
	return backend.ReleaseLease(context.Background(), core.ReleaseLeaseRequest{Lease: lease})
}

func TestStopDeletesVMWhoseAcquireDiedAfterCreate(t *testing.T) {
	isolateCrabboxState(t)
	cloud := &fakeCloud{vms: map[string]string{}}
	backend := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: io.Discard, Exec: cloud}}
	if _, err := backend.Acquire(context.Background(), core.AcquireRequest{RequestedSlug: "moor-7"}); err == nil {
		t.Fatal("acquire succeeded with a killed provider")
	}
	if len(cloud.vms) != 1 {
		t.Fatalf("vms=%v, want the one the provider created", cloud.vms)
	}
	if err := stopLease(cloud, "moor-7"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if len(cloud.vms) != 0 {
		t.Fatalf("vms=%v survived the stop", cloud.vms)
	}
	routings, err := core.ListExternalRoutings()
	if err != nil || len(routings) != 0 {
		t.Fatalf("routings=%d err=%v survived the stop", len(routings), err)
	}
}

func TestStopWithNoLocalRecordIsNotFound(t *testing.T) {
	isolateCrabboxState(t)
	cloud := &fakeCloud{vms: map[string]string{}}
	err := stopLease(cloud, "moor-7")
	if code := core.ExitCodeForError(err, -1); code != 4 {
		t.Fatalf("exit=%d err=%v, want exit 4", code, err)
	}
}

func TestAcquireKeepsRoutingWhenRollbackFails(t *testing.T) {
	isolateCrabboxState(t)
	runner := &sequenceRunner{responses: []string{
		`{"protocolVersion":1,"lease":{"leaseId":"cbx_ffffffffffff","cloudId":"vm1",` + unclaimedSSH + `}}`,
		`{"protocolVersion":1,"error":"provider api unavailable"}`,
	}}
	backend := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: io.Discard, Exec: runner}}
	if _, err := backend.Acquire(context.Background(), core.AcquireRequest{RequestedSlug: "moor-7"}); err == nil {
		t.Fatal("want identity mismatch")
	}
	if len(runner.operations) != 2 || runner.operations[1] != "release" {
		t.Fatalf("operations=%v", runner.operations)
	}
	path, err := core.ExternalRoutingPath(runner.requests[0].Desired.LeaseID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("routing of a VM the rollback could not release is gone: %v", err)
	}
}

func leaveRouting(t *testing.T, leaseID, slug string) {
	t.Helper()
	lease := core.ExternalRoutedLease{LeaseID: leaseID, Slug: slug, Name: core.LeaseProviderName(leaseID, slug)}
	if _, err := core.PersistValidatedExternalLeaseRouting(lease, testConfig().External); err != nil {
		t.Fatal(err)
	}
}

func TestStopWithTwoRoutingsForOneSlugExitsFive(t *testing.T) {
	isolateCrabboxState(t)
	leaveRouting(t, "cbx_00000000000a", "moor-7")
	leaveRouting(t, "cbx_00000000000b", "moor-7")
	err := stopLease(&fakeCloud{vms: map[string]string{}}, "moor-7")
	if code := core.ExitCodeForError(err, -1); code != 5 || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("exit=%d err=%v, want exit 5 naming the ambiguity", code, err)
	}
}

func TestAcquireSkipsSlugWithLeftoverRouting(t *testing.T) {
	isolateCrabboxState(t)
	leaveRouting(t, "cbx_00000000000a", "moor-7")
	cloud := &fakeCloud{vms: map[string]string{}}
	backend := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: io.Discard, Exec: cloud}}
	_, err := backend.Acquire(context.Background(), core.AcquireRequest{RequestedSlug: "moor-7"})
	if code := core.ExitCodeForError(err, -1); code != 4 || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("exit=%d err=%v, want the slug refused", code, err)
	}
	if len(cloud.vms) != 0 {
		t.Fatalf("vms=%v, provider was asked to create a second VM under the slug", cloud.vms)
	}

	leaseID := "cbx_00000000000b"
	leftover := core.NewLeaseSlug(leaseID)
	leaveRouting(t, "cbx_00000000000c", leftover)
	slug, reservation, err := backend.allocateLeaseSlug(leaseID, "")
	if err != nil {
		t.Fatal(err)
	}
	reservation.Release()
	if slug == leftover {
		t.Fatalf("allocated slug %q that a leftover routing still holds", slug)
	}
}

func TestStopClearsRoutingOfLeaseProviderNoLongerHas(t *testing.T) {
	isolateCrabboxState(t)
	leaveRouting(t, "cbx_00000000000a", "moor-7")
	cloud := &fakeCloud{vms: map[string]string{}}
	err := stopLease(cloud, "moor-7")
	if code := core.ExitCodeForError(err, -1); code != 4 || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("exit=%d err=%v, want exit 4 not found", code, err)
	}
	routings, err := core.ListExternalRoutings()
	if err != nil || len(routings) != 0 {
		t.Fatalf("routings=%d err=%v survived a stop the provider answered not found", len(routings), err)
	}
	backend := &leaseBackend{cfg: testConfig(), rt: core.Runtime{Stderr: io.Discard, Exec: cloud}}
	_, _ = backend.Acquire(context.Background(), core.AcquireRequest{RequestedSlug: "moor-7"})
	if len(cloud.vms) != 1 {
		t.Fatalf("vms=%v, want the slug free for a new acquire", cloud.vms)
	}
}
