package azuresandbox

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	core "github.com/openclaw/crabbox/internal/cli"
	"github.com/openclaw/crabbox/internal/testutil"
)

type fixture struct {
	created                           createRequest
	box                               *sandbox
	creates, deletes                  int
	loseCreateResponse, hideInventory bool
}

func (f *fixture) request(t *testing.T, r *http.Request) (*http.Response, error) {
	t.Helper()
	if r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/sandboxes") {
		f.creates++
		var body createRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		f.created = body
		f.box = &sandbox{ID: "unique-resource", State: "Running", Labels: body.Labels}
		if f.loseCreateResponse {
			return response(500, "uncertain"), nil
		}
		data, _ := json.Marshal(f.box)
		return response(200, string(data)), nil
	}
	if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/sandboxes") {
		boxes := []sandbox{}
		if f.box != nil && !f.hideInventory {
			boxes = append(boxes, *f.box)
		}
		data, _ := json.Marshal(boxes)
		return response(200, string(data)), nil
	}
	if r.Method == "DELETE" {
		f.deletes++
		f.box = nil
		return response(204, ""), nil
	}
	if f.box == nil {
		return response(404, ""), nil
	}
	data, _ := json.Marshal(f.box)
	return response(200, string(data)), nil
}

func TestPreparedDiskKeepsFreshLeaseOwnershipAndRejectsSourceChange(t *testing.T) {
	f := &fixture{}
	b, req := fixtureBackend(t, f)
	b.cfg.AzureSandbox.DiskID = "prepared-disk-id"
	if _, err := b.acquire(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(f.created.SourcesRef)
	if err != nil || string(wire) != `{"diskImage":{"id":"prepared-disk-id"}}` {
		t.Fatalf("private disk reference = %s, error = %v", wire, err)
	}
	if f.created.Labels["crabbox_lease"] != req.RequestedLeaseID || f.created.Labels["crabbox_attempt"] == "" || f.created.Lifecycle["autoSuspendPolicy"] == nil {
		t.Fatal("prepared disk lost fresh ownership or lifecycle")
	}
	b.cfg.AzureSandbox.DiskID = "replacement-disk-id"
	if _, err := b.acquire(t.Context(), req); err == nil || f.creates != 1 {
		t.Fatal("lease replay changed its prepared disk")
	}
	if err := b.Stop(t.Context(), core.StopRequest{ID: req.RequestedLeaseID}); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedDiskRejectsConflictingPublicSource(t *testing.T) {
	f := &fixture{}
	b, req := fixtureBackend(t, f)
	b.cfg.AzureSandbox.DiskID, b.cfg.AzureSandbox.Disk = "prepared-disk-id", "other-public-image"
	if _, err := b.acquire(t.Context(), req); err == nil || f.creates != 0 {
		t.Fatal("ambiguous disk source allocated")
	}
}

func fixtureBackend(t *testing.T, f *fixture) (*backend, core.FixedWarmupRequest) {
	t.Helper()
	testutil.IsolateUserDirs(t)
	cfg := core.BaseConfig()
	cfg.Provider, cfg.TTL, cfg.IdleTimeout = providerName, 4*time.Hour, 15*time.Minute
	cfg.AzureSandbox = core.AzureSandboxConfig{Region: "westus3", Subscription: "subscription", ResourceGroup: "workers", Group: "sandboxes", Disk: "ubuntu"}
	b := &backend{cfg: cfg, rt: core.Runtime{Stdout: io.Discard, Stderr: io.Discard}, api: fixtureClient(t, func(r *http.Request) (*http.Response, error) { return f.request(t, r) })}
	return b, core.FixedWarmupRequest{RequestedLeaseID: "cbx_abcdef123456", WarmupRequest: core.WarmupRequest{Repo: core.Repo{Root: t.TempDir()}, Keep: true, RequestedSlug: "sandbox"}}
}

func TestFixedSandboxReplayAndExactCleanup(t *testing.T) {
	f := &fixture{}
	b, req := fixtureBackend(t, f)
	for range 2 {
		if _, err := b.acquire(t.Context(), req); err != nil {
			t.Fatal(err)
		}
	}
	if f.creates != 1 {
		t.Fatal("replay allocated twice")
	}
	if _, err := b.Status(t.Context(), core.StatusRequest{ID: req.RequestedLeaseID}); err != nil {
		t.Fatal(err)
	}
	if err := b.Stop(t.Context(), core.StopRequest{ID: req.RequestedLeaseID}); err != nil {
		t.Fatal(err)
	}
	if f.deletes != 1 {
		t.Fatal("wrong deletion count")
	}
	if _, err := b.acquire(t.Context(), req); err == nil {
		t.Fatal("released lease recreated")
	}
	if err := b.Stop(t.Context(), core.StopRequest{ID: req.RequestedLeaseID}); err != nil {
		t.Fatal(err)
	}
	if f.deletes != 1 {
		t.Fatal("terminal replay deleted twice")
	}
}

func TestUncertainCreateAdoptsOriginalAndNeverResubmits(t *testing.T) {
	f := &fixture{loseCreateResponse: true, hideInventory: true}
	b, req := fixtureBackend(t, f)
	if _, err := b.acquire(t.Context(), req); err == nil {
		t.Fatal("uncertain create accepted")
	}
	if _, err := b.acquire(t.Context(), req); err == nil {
		t.Fatal("empty inventory settled uncertainty")
	}
	if err := b.Stop(t.Context(), core.StopRequest{ID: req.RequestedLeaseID}); err == nil {
		t.Fatal("empty inventory proved deletion")
	}
	if f.creates != 1 || f.deletes != 0 {
		t.Fatal("uncertain attempt repeated or deleted")
	}
	f.hideInventory = false
	if _, err := b.acquire(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if f.creates != 1 {
		t.Fatal("adoption allocated a new resource")
	}
}

func TestWrongAttemptCannotBeAdoptedOrDeleted(t *testing.T) {
	f := &fixture{}
	b, req := fixtureBackend(t, f)
	if _, err := b.acquire(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	f.box.Labels["crabbox_attempt"] = "other-owner"
	if _, err := b.acquire(t.Context(), req); err == nil {
		t.Fatal("adopted another owner")
	}
	if err := b.Stop(t.Context(), core.StopRequest{ID: req.RequestedLeaseID}); err == nil {
		t.Fatal("deleted another owner")
	}
	if f.deletes != 0 {
		t.Fatal("foreign resource deletion")
	}
}
