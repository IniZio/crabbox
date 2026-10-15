package namespace

import core "github.com/openclaw/crabbox/internal/cli"

// ApplyReleaseDelete makes release delete the Namespace resource.
func (Provider) ApplyReleaseDelete(cfg *core.Config) {
	cfg.Namespace.DeleteOnRelease = true
	core.MarkDeleteOnReleaseExplicit(cfg, cfg.Provider)
}

var _ core.ProviderReleaseDeleter = Provider{}
