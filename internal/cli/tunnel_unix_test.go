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

// fakeReverseSSH records -R argv and a process pair, and answers the Box
// probe only after probeFailures failed attempts.
func writeFakeReverseSSH(t *testing.T, probeFailures int) (dir string) {
	t.Helper()
	dir = t.TempDir()
	script := `#!/bin/sh
for a in "$@"; do
  [ "$a" = "-G" ] && exit 0
done
case " $* " in
*" -R "*)
  printf '%s\n' "$*" > "$CRABBOX_TEST_DIR/argv"
  sleep 300 &
  echo $! > "$CRABBOX_TEST_DIR/child"
  echo $$ > "$CRABBOX_TEST_DIR/pid"
  echo "secret-user denied" >&2
  wait
  ;;
*)
  n=$(cat "$CRABBOX_TEST_DIR/probes" 2>/dev/null || echo 0)
  n=$((n+1))
  echo $n > "$CRABBOX_TEST_DIR/probes"
  [ "$n" -gt ` + strconv.Itoa(probeFailures) + ` ]
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CRABBOX_TEST_DIR", dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	return dir
}

func TestSSHReverseForwardWaitsForBoxProbeAndReapsProcessGroup(t *testing.T) {
	dir := writeFakeReverseSSH(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout := newSynchronizedBuffer(0)
	done := make(chan error, 1)
	go func() {
		done <- runSSHReverseForward(ctx, SSHTarget{User: "alice", Host: "example.test", Port: "22", DisableHostKeyChecking: true}, "18080", "9000", true, &stdout, 10*time.Second)
	}()
	time.Sleep(100 * time.Millisecond)
	if got := stdout.String(); got != "" {
		t.Fatalf("printed readiness before Box port accepted: %q", got)
	}
	deadline := time.Now().Add(10 * time.Second)
	for stdout.String() == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := strings.TrimSpace(stdout.String()); got != `{"box":18080,"host":9000}` {
		t.Fatalf("ready output=%q", got)
	}
	argv, err := os.ReadFile(filepath.Join(dir, "argv"))
	if err != nil || !strings.Contains(string(argv), "-N -o ExitOnForwardFailure=yes -R 127.0.0.1:18080:127.0.0.1:9000") {
		t.Fatalf("argv=%q err=%v", argv, err)
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
	for name, pid := range pids {
		if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("%s process %d survived cancellation: %v", name, pid, err)
		}
	}
}

func TestSSHReverseForwardTimeoutExit5Redacted(t *testing.T) {
	writeFakeReverseSSH(t, 1<<30)
	stdout := newSynchronizedBuffer(0)
	err := runSSHReverseForward(t.Context(), SSHTarget{User: "secret-user", AuthSecret: true, Host: "example.test", Port: "22", DisableHostKeyChecking: true}, "18080", "9000", true, &stdout, 800*time.Millisecond)
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
