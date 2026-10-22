package external

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestResolveNoSSHWaitOpensNoConnection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		noSSHWait bool
		wantCalls bool
	}{
		{"waits", false, true},
		{"no wait", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateCrabboxState(t)
			dir := t.TempDir()
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + dir + "/calls\"\nexit 0\n"
			if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			reply := `{"protocolVersion":1,"lease":{"leaseId":"cbx_000000000001","slug":"s","name":"n","cloudId":"vm1","ssh":{"user":"u","host":"box.example","port":"22","proxyCommand":"true","readyCheck":"true"}}}`
			backend, _ := resolveStatusBackend(reply)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			lease, err := backend.Resolve(ctx, core.ResolveRequest{ID: "cbx_000000000001", NoLocalStateMutations: true, NoSSHWait: tc.noSSHWait})
			if err != nil {
				t.Fatal(err)
			}
			if lease.SSH.Host != "box.example" {
				t.Fatalf("resolved host %q", lease.SSH.Host)
			}
			_, statErr := os.Stat(filepath.Join(dir, "calls"))
			if called := statErr == nil; called != tc.wantCalls {
				t.Fatalf("ssh invoked=%t, want %t", called, tc.wantCalls)
			}
		})
	}
}
