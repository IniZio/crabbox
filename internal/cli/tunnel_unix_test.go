//go:build !windows

package cli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSSHLocalForwardWaitsForAcceptingListenerAndReapsProcessGroup(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is required for the hermetic loopback listener")
	}
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pid")
	sshPath := filepath.Join(dir, "ssh")
	script := `#!/bin/sh
set -eu
forward=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -L) forward="$2"; shift 2 ;;
    *) shift ;;
  esac
done
port=$(printf '%s' "$forward" | awk -F: '{print $2}')
sleep 0.3
exec "$CRABBOX_TEST_PYTHON" - "$port" "$CRABBOX_TEST_TUNNEL_PID" <<'PY'
import os
import socket
import sys

port = int(sys.argv[1])
pid_path = sys.argv[2]
sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
sock.bind(("127.0.0.1", port))
sock.listen()
with open(pid_path, "w", encoding="utf-8") as handle:
    handle.write(str(os.getpid()))
while True:
    conn, _ = sock.accept()
    conn.close()
PY
`
	if err := os.WriteFile(sshPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CRABBOX_TEST_PYTHON", python)
	t.Setenv("CRABBOX_TEST_TUNNEL_PID", pidPath)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout := newSynchronizedBuffer(0)
	done := make(chan error, 1)
	go func() {
		done <- runSSHLocalForward(ctx, SSHTarget{User: "alice", Host: "example.test", Port: "22", DisableHostKeyChecking: true}, "", "3000", &stdout)
	}()
	time.Sleep(100 * time.Millisecond)
	if got := stdout.String(); got != "" {
		t.Fatalf("printed readiness before listener accepted: %q", got)
	}
	deadline := time.Now().Add(10 * time.Second)
	for stdout.String() == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	url := strings.TrimSpace(stdout.String())
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("ready output=%q", url)
	}
	pidData, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled tunnel err=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled tunnel did not return")
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("SSH tunnel process %d survived cancellation: %v", pid, err)
	}
}

// writeFakeReverseSSH installs an ssh that records -R argv and a process
// pair. Given -v, it prints the reply sample to stderr the way OpenSSH does,
// holding back the forward reply briefly. After a failure reply it exits 255
// like ExitOnForwardFailure, but only 3 s later, so a test can tell the
// reply from the exit. Any other connection is counted in "extra".
func writeFakeReverseSSH(t *testing.T, reply string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	if reply != "" {
		abs, err := filepath.Abs(reply)
		if err != nil {
			t.Fatal(err)
		}
		reply = abs
	}
	script := `#!/bin/sh
for a in "$@"; do
  [ "$a" = "-G" ] && exit 0
done
verbose=false
for a in "$@"; do
  [ "$a" = "-v" ] && verbose=true
done
case " $* " in
*" -R "*)
  printf '%s\n' "$*" > "$CRABBOX_TEST_DIR/argv"
  sleep 300 &
  echo $! > "$CRABBOX_TEST_DIR/child"
  echo $$ > "$CRABBOX_TEST_DIR/pid"
  echo "secret-user denied" >&2
  if $verbose && [ -n "$CRABBOX_TEST_REPLY" ]; then
    sed '/remote forward/,$d' "$CRABBOX_TEST_REPLY" >&2
    sleep 0.3
    sed -n '/remote forward/,$p' "$CRABBOX_TEST_REPLY" >&2
    if grep -q 'remote forward failure' "$CRABBOX_TEST_REPLY"; then
      sleep 3
      kill $!
      exit 255
    fi
  fi
  wait
  ;;
*)
  echo x >> "$CRABBOX_TEST_DIR/extra"
  exit 1
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CRABBOX_TEST_DIR", dir)
	t.Setenv("CRABBOX_TEST_REPLY", reply)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return dir
}

func TestSSHReverseForwardWaitsForForwardReplyAndReapsProcessGroup(t *testing.T) {
	for _, tc := range []struct {
		name, hostEnd, forward, json, plain string
	}{
		{"port", "9000", "-R 127.0.0.1:18080:127.0.0.1:9000 ", `{"box":18080,"host":9000}`, "box 127.0.0.1:18080 -> host 127.0.0.1:9000"},
		{"socket", "/tmp/moor-1000/px-0123456789abcdef", "-R 127.0.0.1:18080:/tmp/moor-1000/px-0123456789abcdef ", `{"box":18080,"socket":"/tmp/moor-1000/px-0123456789abcdef"}`, "box 127.0.0.1:18080 -> host /tmp/moor-1000/px-0123456789abcdef"},
	} {
		for _, asJSON := range []bool{true, false} {
			ready, format := tc.plain, "plain"
			if asJSON {
				ready, format = tc.json, "json"
			}
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				boxPort, host, err := parseTunnelReverse("18080:" + tc.hostEnd)
				if err != nil {
					t.Fatal(err)
				}
				testSSHReverseForwardLifecycle(t, boxPort, host, asJSON, tc.forward, ready, "testdata/ssh-reverse-forward/success-"+tc.name+".txt")
			})
		}
	}
}

func testSSHReverseForwardLifecycle(t *testing.T, boxPort string, host reverseTunnelHost, asJSON bool, forward, ready, reply string) {
	dir := writeFakeReverseSSH(t, reply)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout := newSynchronizedBuffer(0)
	done := make(chan error, 1)
	go func() {
		done <- runSSHReverseForward(ctx, SSHTarget{User: "alice", Host: "example.test", Port: "22", DisableHostKeyChecking: true}, boxPort, host, asJSON, &stdout, 10*time.Second)
	}()
	time.Sleep(100 * time.Millisecond)
	if got := stdout.String(); got != "" {
		t.Fatalf("printed readiness before the forward reply: %q", got)
	}
	deadline := time.Now().Add(10 * time.Second)
	for stdout.String() == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := strings.TrimSpace(stdout.String()); got != ready {
		t.Fatalf("ready output=%q, want %q", got, ready)
	}
	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	if err != nil || !strings.Contains(string(argv), "-v -N -o ExitOnForwardFailure=yes "+forward) {
		t.Fatalf("argv=%q err=%v, want %q", argv, err, forward)
	}
	if extra, err := os.ReadFile(filepath.Join(dir, "extra")); err == nil {
		t.Fatalf("readiness opened %d extra SSH connections", strings.Count(string(extra), "x"))
	}
	pids := map[string]int{}
	for _, name := range []string{"pid", "child"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if pids[name], err = strconv.Atoi(strings.TrimSpace(string(data))); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled reverse tunnel err=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled reverse tunnel did not return")
	}
	// The killed child stays a zombie until its new parent reaps it, and
	// kill(pid, 0) succeeds on a zombie, so allow the reap a few seconds.
	reapDeadline := time.Now().Add(5 * time.Second)
	for name, pid := range pids {
		err := syscall.Kill(pid, 0)
		for !errors.Is(err, syscall.ESRCH) && time.Now().Before(reapDeadline) {
			time.Sleep(20 * time.Millisecond)
			err = syscall.Kill(pid, 0)
		}
		if !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("%s process %d survived cancellation: %v", name, pid, err)
		}
	}
}

func TestSSHReverseForwardTimeoutExit5Redacted(t *testing.T) {
	writeFakeReverseSSH(t, "")
	stdout := newSynchronizedBuffer(0)
	err := runSSHReverseForward(t.Context(), SSHTarget{User: "secret-user", AuthSecret: true, Host: "example.test", Port: "22", DisableHostKeyChecking: true}, "18080", reverseTunnelHost{port: "9000"}, true, &stdout, 800*time.Millisecond)
	var exitErr ExitError
	if err == nil || !AsExitError(err, &exitErr) || exitErr.Code != 5 {
		t.Fatalf("err=%v, want exit 5", err)
	}
	if strings.Contains(err.Error(), "secret-user") {
		t.Fatalf("timeout error leaked target user: %v", err)
	}
	if stdout.String() != "" {
		t.Fatalf("printed readiness on timeout: %q", stdout.String())
	}
}

func TestSSHReverseForwardFailureReplyExit5(t *testing.T) {
	writeFakeReverseSSH(t, "testdata/ssh-reverse-forward/failure.txt")
	stdout := newSynchronizedBuffer(0)
	start := time.Now()
	err := runSSHReverseForward(t.Context(), SSHTarget{User: "alice", Host: "example.test", Port: "22", DisableHostKeyChecking: true}, "18080", reverseTunnelHost{port: "9000"}, true, &stdout, 10*time.Second)
	var exitErr ExitError
	if err == nil || !AsExitError(err, &exitErr) || exitErr.Code != 5 {
		t.Fatalf("err=%v, want exit 5", err)
	}
	if !strings.Contains(err.Error(), "remote forward failure for: listen 127.0.0.1:18080") {
		t.Fatalf("err=%v, want the refused forward named", err)
	}
	if strings.Contains(err.Error(), "debug1:") {
		t.Fatalf("err carried ssh debug lines: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("refused forward took %s to fail", elapsed)
	}
	if stdout.String() != "" {
		t.Fatalf("printed readiness for a refused forward: %q", stdout.String())
	}
}
