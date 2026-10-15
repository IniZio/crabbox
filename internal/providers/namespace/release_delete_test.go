package namespace

import (
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestApplyReleaseDelete(t *testing.T) {
	cfg := core.Config{Provider: "namespace-devbox"}
	Provider{}.ApplyReleaseDelete(&cfg)
	if !cfg.Namespace.DeleteOnRelease || !core.DeleteOnReleaseExplicit(cfg, cfg.Provider) {
		t.Fatalf("delete=%v explicit=%v", cfg.Namespace.DeleteOnRelease, core.DeleteOnReleaseExplicit(cfg, cfg.Provider))
	}
}
