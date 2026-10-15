package morph

import core "github.com/openclaw/crabbox/internal/cli"

// ApplyReleaseDelete makes release delete the Morph resource.
func (Provider) ApplyReleaseDelete(cfg *core.Config) {
	cfg.Morph.DeleteOnRelease = true
	core.MarkDeleteOnReleaseExplicit(cfg, cfg.Provider)
}

var _ core.ProviderReleaseDeleter = Provider{}
