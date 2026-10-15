package sealosdevbox

import (
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestApplyReleaseDelete(t *testing.T) {
	cfg := core.Config{Provider: "sealos-devbox"}
	Provider{}.ApplyReleaseDelete(&cfg)
	if !cfg.SealosDevbox.DeleteOnRelease || !core.DeleteOnReleaseExplicit(cfg, cfg.Provider) {
		t.Fatalf("delete=%v explicit=%v", cfg.SealosDevbox.DeleteOnRelease, core.DeleteOnReleaseExplicit(cfg, cfg.Provider))
	}
}
