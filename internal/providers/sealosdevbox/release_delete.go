package sealosdevbox

import core "github.com/openclaw/crabbox/internal/cli"

// ApplyReleaseDelete makes release delete the Sealos devbox resource.
func (Provider) ApplyReleaseDelete(cfg *core.Config) {
	cfg.SealosDevbox.DeleteOnRelease = true
	core.MarkDeleteOnReleaseExplicit(cfg, cfg.Provider)
}

var _ core.ProviderReleaseDeleter = Provider{}
