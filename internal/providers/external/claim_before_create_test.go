package external

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
