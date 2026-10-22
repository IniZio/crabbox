//go:build darwin || linux

package cli

import (
	"bytes"
	"path/filepath"
	"slices"
	"testing"
)

// knownHostsPinnedFixture is a lease an earlier call pinned: the provider now
// resolves it with no host key and no client key, so only the per-lease
// known_hosts names the trusted host.
func knownHostsPinnedFixture(t *testing.T) (*forwardSSHServer, SSHTarget) {
	t.Helper()
	server, target := pinnedProxyFixture(t)
	target.Key, target.SSHHostKey = "", ""
	return server, target
}

func repinKnownHosts(t *testing.T, target SSHTarget, hostKey string) {
	t.Helper()
	repin := target
	repin.SSHHostKey = hostKey
	if err := prepareLeaseSSHTrust(&repin, invocationMasterLeaseID); err != nil {
		t.Fatal(err)
	}
}

func TestPinnedLeaseMasterServesLeaseWithoutKeys(t *testing.T) {
	server, target := knownHostsPinnedFixture(t)
	ctx, _ := withSSHControls(t)
	prepared := pinnedProxyTarget(t, ctx, target)
	if prepared.ownerControlPath == "" {
		t.Fatal("known_hosts-pinned lease did not get a master")
	}
	args := sshBaseArgs(prepared)
	if !slices.Contains(args, "ControlPath="+prepared.ownerControlPath) || slices.Contains(args, "ControlPath=none") {
		t.Fatalf("call does not ride the master: %q", args)
	}
	if !slices.Contains(args, "StrictHostKeyChecking=yes") || !slices.Contains(args, "UserKnownHostsFile="+sshConfigFileValue(target.KnownHostsFile)) {
		t.Fatalf("master args lost the lease pin: %q", args)
	}
	for range 3 {
		if err := runSSHQuiet(ctx, target, "exit 0"); err != nil {
			t.Fatal(err)
		}
	}
	if err := runThroughTransportConfig(ctx, target); err != nil {
		t.Fatal(err)
	}
	if got := server.handshakes(); got != 1 {
		t.Fatalf("three commands and one copy used %d authentications, want one", got)
	}
}

func TestPinnedLeaseMasterRefusesMismatchedHostKey(t *testing.T) {
	server, target := knownHostsPinnedFixture(t)
	repinKnownHosts(t, target, newForwardSSHServer(t, "fixture").hostKey)
	ctx, _ := withSSHControls(t)
	if got := pinnedProxyTarget(t, ctx, target); got.ownerControlPath != "" {
		t.Fatal("a mismatched host key started a master")
	}
	if err := runSSHQuiet(ctx, target, "exit 0"); err == nil {
		t.Fatal("a mismatched host key ran a command")
	}
	if got := server.handshakes(); got != 0 {
		t.Fatalf("a mismatched host key authenticated %d times, want none", got)
	}
}

func TestPinnedLeaseMasterRefusesRepinnedKnownHosts(t *testing.T) {
	server, target := knownHostsPinnedFixture(t)
	ctx, _ := withSSHControls(t)
	if got := pinnedProxyTarget(t, ctx, target); got.ownerControlPath == "" {
		t.Fatal("known_hosts-pinned lease did not get a master")
	}
	repinKnownHosts(t, target, newForwardSSHServer(t, "fixture").hostKey)
	if got := pinnedProxyTarget(t, ctx, target); got.ownerControlPath != "" {
		t.Fatal("a re-pinned known_hosts reused or created a master")
	}
	if err := runSSHQuiet(ctx, target, "exit 0"); err == nil {
		t.Fatal("a re-pinned known_hosts reached the old host")
	}
	if got := server.handshakes(); got != 1 {
		t.Fatalf("re-pinned lease reached the server %d times after the master, want none", got-1)
	}
}

func TestStopDeleteOnKnownHostsPinnedLeaseUsesOneConnection(t *testing.T) {
	clearConfigEnv(t)
	server, target := knownHostsPinnedFixture(t)
	t.Setenv("CRABBOX_CONFIG", filepath.Join(t.TempDir(), "missing.yaml"))
	repo, err := findRepo()
	if err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig()
	cfg.Provider = runEnvProfileTestProvider{}.Spec().Name
	lease := LeaseTarget{
		LeaseID: invocationMasterLeaseID,
		Server: Server{CloudID: "pinned-box", Provider: cfg.Provider, Labels: map[string]string{
			"lease": invocationMasterLeaseID, "provider": cfg.Provider, "slug": "pinned-box", "state": "ready",
		}},
		SSH: target,
	}
	if err := ClaimLeaseTargetForRepoConfig(lease.LeaseID, "pinned-box", cfg, lease.Server, lease.SSH, repo.Root, cfg.IdleTimeout, false); err != nil {
		t.Fatal(err)
	}
	runEnvProfileTestAcquireLease = func(AcquireRequest) (LeaseTarget, error) { return lease, nil }
	t.Cleanup(func() {
		runEnvProfileTestAcquireLease = nil
		RemoveLeaseClaim(lease.LeaseID)
	})
	var stdout, stderr bytes.Buffer
	if err := (App{Stdout: &stdout, Stderr: &stderr}).Run(t.Context(), []string{"stop", "--provider", cfg.Provider, "--delete", lease.LeaseID}); err != nil {
		t.Fatalf("stop: %v\nstderr=%s", err, stderr.String())
	}
	if got := server.handshakes(); got != 1 {
		t.Fatalf("stop --delete authenticated %d times, want one\nstderr=%s", got, stderr.String())
	}
}
