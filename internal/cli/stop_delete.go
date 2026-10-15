package cli

// ProviderReleaseDeleter is implemented by providers whose release can either
// retain or delete the resource. ApplyReleaseDelete switches the provider's own
// config to delete on release, marking the choice explicit.
type ProviderReleaseDeleter interface {
	ApplyReleaseDelete(cfg *Config)
}

// applyStopDelete makes a release delete the resource instead of pausing or
// retaining it. Providers without ProviderReleaseDeleter already delete on
// release or have no retain mode, so it is a no-op for them.
func applyStopDelete(cfg *Config) {
	provider, err := ProviderFor(cfg.Provider)
	if err != nil {
		return
	}
	if deleter, ok := provider.(ProviderReleaseDeleter); ok {
		deleter.ApplyReleaseDelete(cfg)
	}
}
