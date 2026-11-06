package external

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestListForwardsProviderResumeMemoryMiB(t *testing.T) {
	isolateCrabboxState(t)
	runner := &recordingRunner{stdout: `{"protocolVersion":1,"leases":[` +
		`{"leaseId":"cbx_000000000001","name":"with-mem","resumeMemoryMiB":2048},` +
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
	if got := servers[0].ResumeMemoryMiB; got == nil || *got != 2048 {
		t.Fatalf("resumeMemoryMiB=%v, want 2048", got)
	}
	if servers[1].ResumeMemoryMiB != nil {
		t.Fatalf("resumeMemoryMiB=%v, want absent", *servers[1].ResumeMemoryMiB)
	}
	withMem, err := json.Marshal(servers[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(withMem), `"resumeMemoryMiB":2048`) {
		t.Fatalf("list JSON dropped resumeMemoryMiB: %s", withMem)
	}
	withoutMem, err := json.Marshal(servers[1])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(withoutMem), "resumeMemoryMiB") {
		t.Fatalf("list JSON invented resumeMemoryMiB: %s", withoutMem)
	}
}
