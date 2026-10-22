package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func sshInvocations(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("CRABBOX_FAKE_SSH_LOG"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n---\n")
}

func TestSSHNoProbePrintsCommandWithoutConnecting(t *testing.T) {
	lease, _ := setupRunClaimSnapshotTest(t)
	touches := 0
	runEnvProfileTestTouchHook = func(TouchRequest) error {
		touches++
		return nil
	}
	t.Cleanup(func() { runEnvProfileTestTouchHook = nil })
	print := func(extra ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		args := append([]string{"--provider", runEnvProfileTestProvider{}.Spec().Name, "--id", lease.LeaseID}, extra...)
		if err := (App{Stdout: &stdout, Stderr: &stderr}).ssh(t.Context(), args); err != nil {
			t.Fatalf("ssh %v: %v\nstderr=%s", extra, err, stderr.String())
		}
		return stdout.String()
	}

	probed := print()
	probes := sshInvocations(t)
	if probes == 0 || touches != 1 {
		t.Fatalf("plain ssh ran %d ssh processes and %d touches, want at least one probe and one touch", probes, touches)
	}
	unprobed := print("--no-probe")
	if unprobed != probed {
		t.Fatalf("--no-probe printed\n%s\nwant\n%s", unprobed, probed)
	}
	if got := sshInvocations(t) - probes; got != 0 {
		t.Fatalf("--no-probe ran %d ssh processes, want none", got)
	}
	if touches != 2 {
		t.Fatalf("--no-probe touched the lease %d times, want once", touches-1)
	}
}
