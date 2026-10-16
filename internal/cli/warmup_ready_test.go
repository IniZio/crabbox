package cli

import "testing"

func TestWarmupReadyLineIncludesRunWorkdir(t *testing.T) {
	cfg := defaultConfig()
	target := SSHTarget{Host: "10.0.0.1", Port: "2222"}
	got := warmupReadyLine(cfg, "crabbox", target, NetworkMode("public"), "cbx_abc", "repo", false)
	want := "ready ssh=crabbox@10.0.0.1:2222 network=public workroot=" + cfg.WorkRoot + " workdir=" + remoteJoin(cfg, "cbx_abc", "repo")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestWarmupReadyLineOmitsWorkdirForActionsRunner(t *testing.T) {
	cfg := defaultConfig()
	target := SSHTarget{Host: "10.0.0.1", Port: "2222"}
	got := warmupReadyLine(cfg, "crabbox", target, NetworkMode("public"), "cbx_abc", "repo", true)
	want := "ready ssh=crabbox@10.0.0.1:2222 network=public workroot=" + cfg.WorkRoot
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
