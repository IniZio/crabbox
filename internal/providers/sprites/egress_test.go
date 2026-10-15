package sprites

import (
	"reflect"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestApplyEgressAllow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		existing []string
		want     []string
	}{
		{name: "generic only", want: []string{"a.example", "*.b.example"}},
		{name: "provider-specific wins", existing: []string{"x.example"}, want: []string{"x.example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cfg core.Config
			cfg.Sprites.NetworkAllow = tc.existing
			if err := (Provider{}).ApplyEgressAllow(&cfg, []string{"a.example", "*.b.example"}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg.Sprites.NetworkAllow, tc.want) {
				t.Fatalf("got %v want %v", cfg.Sprites.NetworkAllow, tc.want)
			}
		})
	}
}
