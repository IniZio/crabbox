package namespace

import (
	"strings"

	core "github.com/openclaw/crabbox/internal/cli"
)

// ApplyEgressAllow maps the generic --egress-allow list onto the Namespace
// egress domains unless they were already set explicitly.
func (Provider) ApplyEgressAllow(cfg *core.Config, domains []string) error {
	if strings.TrimSpace(cfg.Namespace.EgressDomains) == "" {
		cfg.Namespace.EgressDomains = strings.Join(domains, ",")
	}
	return nil
}

var _ core.ProviderEgressAllower = Provider{}
