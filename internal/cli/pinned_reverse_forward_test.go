//go:build darwin || linux

package cli

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const pinnedReverseBoxPort = 18080

func greetingSocket(t *testing.T, greeting string) reverseTunnelHost {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cbx-rf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "host.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte(greeting))
			_ = conn.Close()
		}
	}()
	return reverseTunnelHost{socket: path}
}

type reverseForwardRun struct {
	stdout synchronizedBuffer
	done   chan error
	cancel context.CancelFunc
}

func startPinnedReverseForward(t *testing.T, ctx context.Context, target SSHTarget, host reverseTunnelHost) *reverseForwardRun {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	run := &reverseForwardRun{stdout: newSynchronizedBuffer(0), done: make(chan error, 1), cancel: cancel}
	t.Cleanup(cancel)
	go func() {
		run.done <- runSSHReverseForward(ctx, target, "18080", host, true, &run.stdout, 10*time.Second)
	}()
	return run
}

func (r *reverseForwardRun) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for r.stdout.String() == "" {
		select {
		case err := <-r.done:
			t.Fatalf("reverse forward ended before ready: %v", err)
		case <-deadline:
			t.Fatal("reverse forward did not become ready")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestPinnedReverseForwardRidesTheInvocationMaster(t *testing.T) {
	server, target := pinnedProxyFixture(t)
	server.acceptRemoteForwards()
	ctx, _ := withSSHControls(t)
	// Resolving the lease checks readiness before the tunnel starts.
	if err := runSSHQuiet(ctx, target, "exit 0"); err != nil {
		t.Fatal(err)
	}
	run := startPinnedReverseForward(t, ctx, target, greetingSocket(t, "hello-from-host\n"))
	run.waitReady(t)
	if got, err := server.dialRemoteForward(pinnedReverseBoxPort); err != nil || got != "hello-from-host\n" {
		t.Fatalf("box end of the forward read %q, %v", got, err)
	}
	if got := server.handshakes(); got != 1 {
		t.Fatalf("readiness check and reverse forward used %d authentications, want one", got)
	}
	run.cancel()
	select {
	case err := <-run.done:
		if err != nil {
			t.Fatalf("cancelled reverse forward: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled reverse forward did not return")
	}
}

func TestPinnedReverseForwardEndsWithItsTransport(t *testing.T) {
	server, target := pinnedProxyFixture(t)
	server.acceptRemoteForwards()
	ctx, _ := withSSHControls(t)
	run := startPinnedReverseForward(t, ctx, target, greetingSocket(t, "hello-from-host\n"))
	run.waitReady(t)
	server.mu.Lock()
	for _, conn := range server.conns {
		_ = conn.Close()
	}
	server.mu.Unlock()
	select {
	case err := <-run.done:
		if err == nil {
			t.Fatal("reverse forward reported success after its transport closed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("reverse forward outlived its transport")
	}
}

func TestPinnedReverseForwardRefusesChangedPin(t *testing.T) {
	server, target := pinnedProxyFixture(t)
	server.acceptRemoteForwards()
	ctx, _ := withSSHControls(t)
	if err := runSSHQuiet(ctx, target, "exit 0"); err != nil {
		t.Fatal(err)
	}
	other := newForwardSSHServer(t, "fixture")
	repinned := target
	repinned.SSHHostKey = other.hostKey
	if err := prepareLeaseSSHTrust(&repinned, invocationMasterLeaseID); err != nil {
		t.Fatal(err)
	}
	run := startPinnedReverseForward(t, ctx, repinned, greetingSocket(t, "hello-from-host\n"))
	select {
	case err := <-run.done:
		if err == nil {
			t.Fatal("a changed pin opened the reverse forward")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("changed pin did not fail the reverse forward")
	}
	if _, err := server.dialRemoteForward(pinnedReverseBoxPort); err == nil {
		t.Fatal("a changed pin set up the forward over the old master")
	}
	if got := server.handshakes(); got != 1 {
		t.Fatalf("changed pin reached the server %d times after the master, want none", got-1)
	}
}

func onlyMaster(t *testing.T, controls *serveRelayControls) serveRelayControl {
	t.Helper()
	controls.mu.Lock()
	defer controls.mu.Unlock()
	if len(controls.entries) != 1 {
		t.Fatalf("invocation holds %d masters, want one", len(controls.entries))
	}
	for _, entry := range controls.entries {
		return entry
	}
	return serveRelayControl{}
}

func processesUsing(path string) []string {
	var found []string
	procs, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, p := range procs {
		data, err := os.ReadFile(p)
		if err == nil && strings.Contains(string(data), path) {
			found = append(found, strings.ReplaceAll(string(data), "\x00", " "))
		}
	}
	return found
}

func requireMasterClosed(t *testing.T, controls *serveRelayControls, master serveRelayControl) {
	t.Helper()
	if err := controls.closeLease(""); err != nil {
		t.Logf("closeLease: %v", err)
	}
	select {
	case <-master.done:
	case <-time.After(5 * time.Second):
		t.Fatal("master still running after the invocation closed its controls")
	}
	if _, err := os.Stat(filepath.Dir(master.path)); !os.IsNotExist(err) {
		t.Fatalf("control dir survived: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		left := processesUsing(master.path)
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("orphan processes: %q", left)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitTunnelExit(t *testing.T, run *reverseForwardRun, wantCode int) error {
	t.Helper()
	select {
	case err := <-run.done:
		if got := ExitCodeForError(err, 0); got != wantCode {
			t.Fatalf("tunnel ended with %v (code %d), want code %d", err, got, wantCode)
		}
		return err
	case <-time.After(15 * time.Second):
		t.Fatal("tunnel did not return")
	}
	return nil
}

func TestPinnedReverseForwardExitsFiveWhenMasterExits(t *testing.T) {
	server, target := pinnedProxyFixture(t)
	server.acceptRemoteForwards()
	ctx, controls := withSSHControls(t)
	if err := runSSHQuiet(ctx, target, "exit 0"); err != nil {
		t.Fatal(err)
	}
	run := startPinnedReverseForward(t, ctx, target, greetingSocket(t, "hi\n"))
	run.waitReady(t)
	master := onlyMaster(t, controls)
	select {
	case err := <-run.done:
		t.Fatalf("tunnel returned while master alive: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
	if out, err := exec.Command(directSSHExecutable(), "-F", "none", "-S", master.path, "-O", "exit", sshTransportHostAlias).CombinedOutput(); err != nil {
		t.Fatalf("ssh -O exit: %v %s", err, out)
	}
	waitTunnelExit(t, run, 5)
	requireMasterClosed(t, controls, master)
}

func TestPinnedReverseForwardRefusedFailsFast(t *testing.T) {
	server, target := pinnedProxyFixture(t)
	ctx, controls := withSSHControls(t)
	if err := runSSHQuiet(ctx, target, "exit 0"); err != nil {
		t.Fatal(err)
	}
	run := startPinnedReverseForward(t, ctx, target, greetingSocket(t, "hi\n"))
	err := waitTunnelExit(t, run, 5)
	if !strings.Contains(err.Error(), "refused") {
		t.Fatalf("refusal message %q", err)
	}
	if run.stdout.String() != "" {
		t.Fatalf("refused forward printed readiness %q", run.stdout.String())
	}
	if got := server.handshakes(); got != 1 {
		t.Fatalf("refused forward used %d authentications, want one", got)
	}
	requireMasterClosed(t, controls, onlyMaster(t, controls))
}

func TestPinnedReverseForwardLeavesNothingAfterCancel(t *testing.T) {
	server, target := pinnedProxyFixture(t)
	server.acceptRemoteForwards()
	ctx, controls := withSSHControls(t)
	run := startPinnedReverseForward(t, ctx, target, greetingSocket(t, "hi\n"))
	run.waitReady(t)
	master := onlyMaster(t, controls)
	run.cancel()
	waitTunnelExit(t, run, 0)
	requireMasterClosed(t, controls, master)
	if _, err := server.dialRemoteForward(pinnedReverseBoxPort); err == nil {
		t.Fatal("box end of the forward still answers after the invocation closed")
	}
}
