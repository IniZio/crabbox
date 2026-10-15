package morph

import (
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestApplyReleaseDelete(t *testing.T) {
	cfg := core.Config{Provider: "morph"}
	Provider{}.ApplyReleaseDelete(&cfg)
	if !cfg.Morph.DeleteOnRelease || !core.DeleteOnReleaseExplicit(cfg, cfg.Provider) {
		t.Fatalf("delete=%v explicit=%v", cfg.Morph.DeleteOnRelease, core.DeleteOnReleaseExplicit(cfg, cfg.Provider))
	}
}
