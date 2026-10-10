package ssh

import (
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func staticPurgeCommands(f staticCommandFixture, t *testing.T) []string {
	var out []string
	for _, c := range f.commands(t) {
		if strings.HasPrefix(c.Command, "rm -rf -- ") {
			out = append(out, c.Command)
		}
	}
	return out
}

func TestStaticSSHStopPurgesWorkspace(t *testing.T) {
	f := newStaticCommandFixture(t, core.TargetLinux)
	f.acquire(t)
	f.run(t, "stop", "--id", f.cfg.Static.ID)
	purges := staticPurgeCommands(f, t)
	if len(purges) != 1 {
		t.Fatalf("purge commands=%d, want 1: %q", len(purges), purges)
	}
	for _, want := range []string{
		"rm -rf -- '/work/fixture/" + f.cfg.Static.ID + "'",
		".crabbox/workspace-owners/",
		".gate'",
		".crabbox/actions/" + f.cfg.Static.ID + ".stop'",
	} {
		if !strings.Contains(purges[0], want) {
			t.Fatalf("purge command missing %q: %s", want, purges[0])
		}
	}
	if strings.Contains(purges[0], f.other.LeaseID) {
		t.Fatalf("purge touched unrelated lease: %s", purges[0])
	}
}

func TestStaticSSHStopPurgeFailureStillReleases(t *testing.T) {
	f := newStaticCommandFixture(t, core.TargetLinux)
	f.acquire(t)
	f.setFailures(t, false, true) // fake ssh fails commands naming the .stop marker, including the purge
	stderr := f.run(t, "stop", "--id", f.cfg.Static.ID)
	if !strings.Contains(stderr, "warning: remote workspace cleanup failed") {
		t.Fatalf("missing purge warning:\n%s", stderr)
	}
	if _, exists, err := core.ReadLeaseClaimWithPresence(f.cfg.Static.ID); err != nil || exists {
		t.Fatalf("claim must be released after failed purge: exists=%t err=%v", exists, err)
	}
}

func TestStaticSSHOneShotRunDoesNotPurge(t *testing.T) {
	f := newStaticCommandFixture(t, core.TargetLinux)
	f.run(t, "run", "--no-sync", "--no-hydrate", "--", "true")
	if purges := staticPurgeCommands(f, t); len(purges) != 0 {
		t.Fatalf("run must not purge: %q", purges)
	}
}

func TestStaticWorkspacePurgeCommandGuards(t *testing.T) {
	for _, tt := range []struct{ root, id string }{
		{"/work", ""}, {"/work", "."}, {"/work", ".."}, {"/work", "a/b"}, {"/work", "../x"},
		{"/work", "a..b"}, {"/work", `a\b`}, {"", "id"}, {"relative", "id"}, {"/", "id"},
	} {
		if cmd, err := core.StaticWorkspacePurgeCommand(tt.root, tt.id); err == nil {
			t.Errorf("root=%q id=%q: expected refusal, got %q", tt.root, tt.id, cmd)
		}
	}
	cmd, err := core.StaticWorkspacePurgeCommand("/work//root/", "moor-x")
	if err != nil || !strings.HasPrefix(cmd, "rm -rf -- '/work/root/moor-x' &&") {
		t.Fatalf("cmd=%q err=%v", cmd, err)
	}
}
