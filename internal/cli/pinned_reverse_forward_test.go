//go:build darwin || linux

package cli

import (
	"context"
	"net"
	"os"
	"path/filepath"
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
