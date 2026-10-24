package external

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/openclaw/crabbox/internal/cli"
)

const relativeRootLease = `{"protocolVersion":1,"lease":{"leaseId":"cbx_000000000001","slug":"s","name":"n","cloudId":"vm1","ssh":{"user":"u","host":"box.example","port":"22","proxyCommand":"true","readyCheck":"true"}}}`

// installHomeSSH puts an ssh on PATH that runs the remote command locally with
// HOME set to the lease's login home, and records every remote command.
func installHomeSSH(t *testing.T, home string) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nfor a in \"$@\"; do [ \"$a\" = \"-G\" ] && exit 0; last=$a; done\n" +
		"printf '%s\\n' \"$last\" >> \"" + dir + "/calls\"\nHOME=\"$FAKE_LOGIN_HOME\" exec /bin/sh -c \"$last\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_LOGIN_HOME", home)
	return filepath.Join(dir, "calls")
}

const relativeRootAcquireReply = `{"protocolVersion":1,"lease":{"slug":"s","name":"n","cloudId":"vm1","ssh":{"user":"u","host":"box.example","port":"22","proxyCommand":"true","readyCheck":"true"}}}`

func relativeRootBackend(workRoot, reply string) (*leaseBackend, *sequenceRunner) {
	cfg := testConfig()
	cfg.External.WorkRoot = workRoot
	cfg.WorkRoot = workRoot
	runner := &sequenceRunner{responses: []string{reply, `{"protocolVersion":1}`}}
	return &leaseBackend{cfg: cfg, rt: core.Runtime{Stderr: os.Stderr, Exec: runner}}, runner
}

func TestRelativeWorkRootPassesConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		root     string
		target   string
		mode     string
		wantFail string
	}{
		{name: "simple", root: "moor", target: core.TargetLinux},
		{name: "nested", root: "work/moor", target: core.TargetLinux},
		{name: "macos", root: "moor", target: core.TargetMacOS},
		{name: "home itself", root: ".", target: core.TargetLinux, wantFail: "dedicated subdirectory"},
		{name: "escapes home", root: "../moor", target: core.TargetLinux, wantFail: "must stay inside the login home"},
		{name: "windows native", root: "moor", target: core.TargetWindows, mode: core.WindowsModeNormal, wantFail: "absolute Windows path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.External.WorkRoot = tc.root
			cfg.WorkRoot = tc.root
			cfg.TargetOS = tc.target
			cfg.WindowsMode = tc.mode
			err := validateConfig(cfg)
			if tc.wantFail == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantFail) {
				t.Fatalf("err=%v, want %q", err, tc.wantFail)
			}
		})
	}
}

func TestAbsoluteWorkRootSafetyChecksUnchanged(t *testing.T) {
	cfg := testConfig()
	cfg.External.WorkRoot = "/root"
	if err := validateConfig(cfg); err == nil || !strings.Contains(err.Error(), "too broad") {
		t.Fatalf("err=%v", err)
	}
}

func TestAcquireResolvesRelativeWorkRootAgainstLoginHome(t *testing.T) {
	for _, tc := range []struct{ home, want string }{
		{"/root", "/root/moor"},
		{"/home/crabbox", "/home/crabbox/moor"},
	} {
		t.Run(tc.home, func(t *testing.T) {
			isolateCrabboxState(t)
			installHomeSSH(t, tc.home)
			backend, _ := relativeRootBackend("moor", relativeRootAcquireReply)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			lease, err := backend.Acquire(ctx, core.AcquireRequest{RequestedSlug: "s", Keep: true})
			if err != nil {
				t.Fatal(err)
			}
			if got := lease.Server.Labels["work_root"]; got != tc.want {
				t.Fatalf("work_root=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveResolvesRelativeWorkRootAgainstLoginHome(t *testing.T) {
	isolateCrabboxState(t)
	installHomeSSH(t, "/home/crabbox")
	backend, _ := relativeRootBackend("moor", relativeRootLease)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	lease, err := backend.Resolve(ctx, core.ResolveRequest{ID: "cbx_000000000001", NoLocalStateMutations: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := lease.Server.Labels["work_root"]; got != "/home/crabbox/moor" {
		t.Fatalf("work_root=%q", got)
	}
}

func TestResolveWithoutSSHWaitLeavesRelativeRootUnresolved(t *testing.T) {
	isolateCrabboxState(t)
	calls := installHomeSSH(t, "/home/crabbox")
	backend, _ := relativeRootBackend("moor", relativeRootLease)
	_, err := backend.Resolve(context.Background(), core.ResolveRequest{ID: "cbx_000000000001", NoLocalStateMutations: true, NoSSHWait: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(calls); statErr == nil {
		t.Fatal("ssh ran although the caller asked for no SSH wait")
	}
}

func TestAbsoluteWorkRootNeverReadsLoginHome(t *testing.T) {
	isolateCrabboxState(t)
	calls := installHomeSSH(t, "/home/elsewhere")
	backend, _ := relativeRootBackend("/home/tester/crabbox", relativeRootAcquireReply)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	lease, err := backend.Acquire(ctx, core.AcquireRequest{RequestedSlug: "s", Keep: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := lease.Server.Labels["work_root"]; got != "/home/tester/crabbox" {
		t.Fatalf("work_root=%q", got)
	}
	data, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "HOME") {
		t.Fatalf("ssh calls read the login home: %q", data)
	}
}

func TestUnreadableLoginHomeFailsAcquireAndReleasesLease(t *testing.T) {
	for _, tc := range []struct{ name, home string }{
		{"empty", ""},
		{"not absolute", "relative/home"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateCrabboxState(t)
			installHomeSSH(t, tc.home)
			backend, runner := relativeRootBackend("moor", relativeRootAcquireReply)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, err := backend.Acquire(ctx, core.AcquireRequest{RequestedSlug: "s", Keep: false})
			if err == nil || !strings.Contains(err.Error(), "login home") || strings.Contains(err.Error(), "/work/crabbox") {
				t.Fatalf("err=%v", err)
			}
			if n := len(runner.operations); n != 2 || runner.operations[1] != "release" {
				t.Fatalf("operations=%v, want acquire then release", runner.operations)
			}
		})
	}
}
