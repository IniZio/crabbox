package external

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestProtocolFailureSurfacesProviderJSONError(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	isolateCrabboxState(t)
	script := filepath.Join(t.TempDir(), "adapter.sh")
	body := "#!/bin/sh\ncat >/dev/null\nprintf '%s' '{\"error\":\"FAIL socket_path: too long\"}'\necho 'doctor finished' >&2\nexit 3\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.External.Command = script
	cfg.External.Args = nil
	var stderr bytes.Buffer
	backend := &leaseBackend{cfg: cfg, rt: core.Runtime{Stderr: &stderr, Exec: scriptRunner{}}}
	_, err := backend.List(context.Background(), core.ListRequest{})
	var exit core.ExitError
	if !core.AsExitError(err, &exit) || exit.Code != 3 {
		t.Fatalf("err=%v", err)
	}
	if !strings.Contains(exit.Message, "FAIL socket_path: too long") || !strings.Contains(exit.Message, "doctor finished") {
		t.Fatalf("message=%q", exit.Message)
	}
}
