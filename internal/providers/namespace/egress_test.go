package namespace

import (
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestApplyEgressAllow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing string
		want     string
	}{
		{name: "generic only", want: "a.example,b.example"},
		{name: "provider-specific wins", existing: "x.example", want: "x.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg core.Config
			cfg.Namespace.EgressDomains = tc.existing
			if err := (Provider{}).ApplyEgressAllow(&cfg, []string{"a.example", "b.example"}); err != nil {
				t.Fatal(err)
			}
			if cfg.Namespace.EgressDomains != tc.want {
				t.Fatalf("got %q want %q", cfg.Namespace.EgressDomains, tc.want)
			}
		})
	}
}
