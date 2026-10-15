package cli

import "strings"

// ProviderEgressAllower is implemented by providers that can enforce an egress
// allow-list. ApplyEgressAllow maps the provider-neutral --egress-allow domains
// onto the provider's own config; an explicit provider-specific setting wins.
type ProviderEgressAllower interface {
	ApplyEgressAllow(cfg *Config, domains []string) error
}

const egressAllowFlagUsage = "comma-separated egress allow-list domains; mapped to the provider's own setting (sprites-network-allow, namespace-egress-domains), which wins when also set; errors for providers that cannot enforce it (env CRABBOX_EGRESS_ALLOW)"

func parseEgressAllow(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func applyEgressAllow(cfg *Config, value string) error {
	domains := parseEgressAllow(value)
	if len(domains) == 0 {
		return nil
	}
	provider, err := ProviderFor(cfg.Provider)
	if err == nil {
		if allower, ok := provider.(ProviderEgressAllower); ok {
			return allower.ApplyEgressAllow(cfg, domains)
		}
	}
	return Exit(2, "--egress-allow is not supported for provider=%s", cfg.Provider)
}
