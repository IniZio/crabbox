//go:build darwin || linux

package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const invocationMasterLeaseID = "cbx_00112233aabb"

func pinnedProxyFixture(t *testing.T) (*forwardSSHServer, SSHTarget) {
	t.Helper()
	isolateTestUserDirs(t)
	server := newForwardSSHServer(t, "fixture")
	close(server.release)
	server.enableReadinessSessions(t, t.TempDir(), true)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	keyPath, _, err := EnsureTestboxKey(invocationMasterLeaseID)
	if err != nil {
		t.Fatal(err)
	}
	target := SSHTarget{
		Host: "fixture-box", Port: "22", User: "fixture", Key: keyPath, TargetOS: targetLinux,
		FallbackPorts: []string{}, SSHHostKey: server.hostKey, AuthoritativeKnownHosts: true, SSHConfigProxy: true,
		ProxyCommand: ShellQuote(executable) + " " + testTCPRelayArg + " 127.0.0.1:" + strconv.Itoa(server.port()),
	}
	if err := prepareLeaseSSHTrust(&target, invocationMasterLeaseID); err != nil {
		t.Fatal(err)
	}
	return server, target
}

func withSSHControls(t *testing.T) (context.Context, *serveRelayControls) {
	t.Helper()
	controls := &serveRelayControls{}
	t.Cleanup(func() { _ = controls.closeLease("") })
	return context.WithValue(t.Context(), serveRelayControlsKey{}, controls), controls
}

func pinnedProxyTarget(t *testing.T, ctx context.Context, target SSHTarget) SSHTarget {
	t.Helper()
	prepared, err := prepareServeRelayControl(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func (s *forwardSSHServer) handshakes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users)
}

func runThroughTransportConfig(ctx context.Context, target SSHTarget) error {
	session, err := newSSHTransportSession(ctx, target, false)
	if err != nil {
		return err
	}
	defer session.Close()
	args := append(session.commandPrefix(), session.host(), "exit 0")
	return exec.CommandContext(ctx, directSSHExecutable(), args...).Run()
}

func TestPinnedProxyControlServesCommandsAndCopies(t *testing.T) {
	server, target := pinnedProxyFixture(t)
	ctx, _ := withSSHControls(t)
	for range 3 {
		// Each call passes a fresh copy, as sync steps do.
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

func TestPinnedProxyControlKeepsOwnerControlSeparate(t *testing.T) {
	server, target := pinnedProxyFixture(t)
	ctx, _ := withSSHControls(t)
	workload := pinnedProxyTarget(t, ctx, target)
	owner, closeOwner, err := startWorkspaceOwnerControl(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeOwner() })
	if workload.ownerControlPath == "" || owner.ownerControlPath == "" || owner.ownerControlPath == workload.ownerControlPath {
		t.Fatalf("owner must hold its own master: workload=%q owner=%q", workload.ownerControlPath, owner.ownerControlPath)
	}
	for range 2 {
		if err := runSSHQuiet(ctx, target, "exit 0"); err != nil {
			t.Fatal(err)
		}
		if err := runSSHQuiet(ctx, owner, "exit 0"); err != nil {
			t.Fatal(err)
		}
	}
	if got := server.handshakes(); got != 2 {
		t.Fatalf("workload and owner used %d authentications, want two", got)
	}
}

func TestPinnedProxyControlRefusesChangedPin(t *testing.T) {
	server, target := pinnedProxyFixture(t)
	ctx, _ := withSSHControls(t)
	if got := pinnedProxyTarget(t, ctx, target); got.ownerControlPath == "" {
		t.Fatal("pinned target did not get a master")
	}
	other := newForwardSSHServer(t, "fixture")
	repinned := target
	repinned.SSHHostKey = other.hostKey
	if err := prepareLeaseSSHTrust(&repinned, invocationMasterLeaseID); err != nil {
		t.Fatal(err)
	}
	if got := pinnedProxyTarget(t, ctx, repinned); got.ownerControlPath != "" {
		t.Fatal("a changed pin reused or created a master")
	}
	if err := runSSHQuiet(ctx, repinned, "exit 0"); err == nil {
		t.Fatal("a changed pin authenticated the old host")
	}
	if got := server.handshakes(); got != 1 {
		t.Fatalf("changed pin reached the server %d times after the master, want none", got-1)
	}
}

func TestPinnedProxyControlKeyTracksIdentity(t *testing.T) {
	base := SSHTarget{Host: "box", User: "u", Port: "22", Key: "/lease/key", CertificateFile: "/lease/cert",
		SSHHostKey: "ssh-ed25519 AAAA", KnownHostsFile: "/lease/known_hosts", HostKeyAlias: "alias", ProxyCommand: "proxy a"}
	for name, mutate := range map[string]func(*SSHTarget){
		"host":        func(v *SSHTarget) { v.Host = "other" },
		"user":        func(v *SSHTarget) { v.User = "other" },
		"port":        func(v *SSHTarget) { v.Port = "2222" },
		"key":         func(v *SSHTarget) { v.Key = "/other/key" },
		"certificate": func(v *SSHTarget) { v.CertificateFile = "/other/cert" },
		"pin":         func(v *SSHTarget) { v.SSHHostKey = "ssh-ed25519 BBBB" },
		"known_hosts": func(v *SSHTarget) { v.KnownHostsFile = "/other/known_hosts" },
		"alias":       func(v *SSHTarget) { v.HostKeyAlias = "other" },
		"proxy":       func(v *SSHTarget) { v.ProxyCommand = "proxy b" },
	} {
		other := base
		mutate(&other)
		if serveRelayControlKey(base) == serveRelayControlKey(other) {
			t.Errorf("changed %s shared a master", name)
		}
	}
}

func TestPinnedProxyControlEligibility(t *testing.T) {
	base := SSHTarget{Host: "box", User: "u", Port: "22", Key: "/lease/key", TargetOS: targetLinux, SSHConfigProxy: true,
		SSHHostKey: "ssh-ed25519 AAAA", KnownHostsFile: "/lease/known_hosts", HostKeyAlias: "alias", ProxyCommand: "proxy", AuthoritativeKnownHosts: true}
	if !pinnedProxyControlEligible(base) {
		t.Fatal("pinned proxy lease must be eligible")
	}
	resolved := base
	resolved.Key, resolved.SSHHostKey = "", ""
	if !pinnedProxyControlEligible(resolved) {
		t.Fatal("a lease pinned only by its known_hosts must be eligible")
	}
	for name, mutate := range map[string]func(*SSHTarget){
		"windows target":     func(v *SSHTarget) { v.TargetOS = targetWindows },
		"no proxy":           func(v *SSHTarget) { v.ProxyCommand = "" },
		"provider trust off": func(v *SSHTarget) { v.AuthoritativeKnownHosts = false },
		"no known_hosts":     func(v *SSHTarget) { v.KnownHostsFile = "" },
		"no alias":           func(v *SSHTarget) { v.HostKeyAlias = "" },
		"checking disabled":  func(v *SSHTarget) { v.DisableHostKeyChecking = true },
		"provider opt-out":   func(v *SSHTarget) { v.NoControlMaster = true },
		"auth secret":        func(v *SSHTarget) { v.AuthSecret = true },
		"config file":        func(v *SSHTarget) { v.SSHConfigFile = "/provider/config" },
		"config data":        func(v *SSHTarget) { v.SSHConfigData = []byte("Host x\n") },
		"jump route":         func(v *SSHTarget) { v.SSHJump = &SSHJump{Host: "front-door"} },
	} {
		other := base
		mutate(&other)
		if pinnedProxyControlEligible(other) {
			t.Errorf("%s must keep its own connections", name)
		}
	}
}

func TestPinnedProxyControlSkipsLocalForwards(t *testing.T) {
	server, target := pinnedProxyFixture(t)
	ctx, _ := withSSHControls(t)
	session, err := newSSHTransportSession(ctx, target, true)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	data, err := os.ReadFile(session.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "  ControlPath none\n") {
		t.Fatalf("forward config borrowed a master:\n%s", data)
	}
	if got := server.handshakes(); got != 0 {
		t.Fatalf("forward session started %d masters", got)
	}
	workload := pinnedProxyTarget(t, ctx, target)
	if workload.ownerControlPath == "" {
		t.Fatal("workload master did not start")
	}
	forward, err := renderSSHTransportConfigWithRoute(workload, true, sshTransportConfigRoute{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(forward, workload.ownerControlPath) {
		t.Fatal("forward config named the invocation master")
	}
}

func TestPinnedProxyControlClosesOnReleaseAndExit(t *testing.T) {
	_, target := pinnedProxyFixture(t)
	ctx, masters := withSSHControls(t)
	first := pinnedProxyTarget(t, ctx, target)
	if first.ownerControlPath == "" {
		t.Fatal("master did not start")
	}
	if err := CloseLeaseSSHControls(ctx, "cbx_ffeeddccbbaa"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(first.ownerControlPath); err != nil {
		t.Fatalf("another lease's release closed this master: %v", err)
	}
	if err := CloseLeaseSSHControls(ctx, invocationMasterLeaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Dir(first.ownerControlPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released lease kept its master: %v", err)
	}
	if got := pinnedProxyTarget(t, ctx, target); got.ownerControlPath != "" {
		t.Fatal("a released lease started a new master")
	}
	ctx, masters = withSSHControls(t)
	second := pinnedProxyTarget(t, ctx, target)
	if second.ownerControlPath == "" {
		t.Fatal("master did not start")
	}
	if err := masters.closeLease(""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Dir(second.ownerControlPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invocation exit kept its master: %v", err)
	}
	if got := pinnedProxyTarget(t, ctx, target); got.ownerControlPath != "" {
		t.Fatal("a finished invocation started a master")
	}
}

func TestPinnedProxyControlDeadSocketFallsBack(t *testing.T) {
	server, target := pinnedProxyFixture(t)
	ctx, masters := withSSHControls(t)
	dir, err := os.MkdirTemp("/tmp", "cbx-dead-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	dead := filepath.Join(dir, "control")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: dead, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	_ = listener.Close()
	masters.entries = map[[32]byte]serveRelayControl{
		serveRelayControlKey(target): {leaseID: target.HostKeyAlias, path: dead, close: func() error { return nil }},
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := runSSHQuiet(callCtx, target, "exit 0"); err != nil {
		t.Fatalf("command over a dead master: %v", err)
	}
	if err := runThroughTransportConfig(callCtx, target); err != nil {
		t.Fatalf("copy over a dead master: %v", err)
	}
	if got := server.handshakes(); got != 2 {
		t.Fatalf("dead master fallback used %d authentications, want one per call", got)
	}
}
