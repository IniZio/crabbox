package external

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestListForwardsProviderHostMemoryMiB(t *testing.T) {
	isolateCrabboxState(t)
	runner := &recordingRunner{stdout: `{"protocolVersion":1,"leases":[` +
		`{"leaseId":"cbx_000000000001","name":"with-mem","hostMemoryMiB":1108},` +
		`{"leaseId":"cbx_000000000002","name":"without-mem"}]}`}
	backend, err := (Provider{}).Configure(testConfig(), core.Runtime{Exec: runner})
	if err != nil {
		t.Fatal(err)
	}
	servers, err := backend.(*leaseBackend).List(context.Background(), core.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("servers=%d", len(servers))
	}
	if got := servers[0].HostMemoryMiB; got == nil || *got != 1108 {
		t.Fatalf("hostMemoryMiB=%v, want 1108", got)
	}
	if servers[1].HostMemoryMiB != nil {
		t.Fatalf("hostMemoryMiB=%v, want absent", *servers[1].HostMemoryMiB)
	}
	withMem, err := json.Marshal(servers[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withMem), `"hostMemoryMiB":1108`) {
		t.Fatalf("list JSON dropped hostMemoryMiB: %s", withMem)
	}
	withoutMem, err := json.Marshal(servers[1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(withoutMem), "hostMemoryMiB") {
		t.Fatalf("list JSON invented hostMemoryMiB: %s", withoutMem)
	}
}
