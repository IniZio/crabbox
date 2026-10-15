package sprites

import core "github.com/openclaw/crabbox/internal/cli"

// ApplyEgressAllow maps the generic --egress-allow list onto the Sprites
// network allow-list unless it was already set explicitly.
func (Provider) ApplyEgressAllow(cfg *core.Config, domains []string) error {
	if len(cfg.Sprites.NetworkAllow) == 0 {
		cfg.Sprites.NetworkAllow = append([]string(nil), domains...)
	}
	return nil
}

var _ core.ProviderEgressAllower = Provider{}
