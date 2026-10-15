package cli

import (
	"io"
	"strings"
	"testing"
)

func TestParseEgressAllow(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"", ""},
		{" , ", ""},
		{"a.example, *.b.example ,,c.example", "a.example,*.b.example,c.example"},
	} {
		if got := strings.Join(parseEgressAllow(tc.in), ","); got != tc.want {
			t.Errorf("parseEgressAllow(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestEgressAllowUnsupportedProvider(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     string
		args    []string
		wantErr bool
	}{
		{name: "flag", args: []string{"--egress-allow", "a.example"}, wantErr: true},
		{name: "env", env: "a.example,b.example", wantErr: true},
		{name: "flag empty", args: []string{"--egress-allow", ""}},
		{name: "unset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CRABBOX_EGRESS_ALLOW", tc.env)
			cfg := baseConfig()
			cfg.Provider = "hetzner"
			fs := newFlagSet("warmup", io.Discard)
			flags := registerLeaseCreateFlags(fs, cfg)
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			err := applyLeaseCreateFlags(&cfg, fs, flags)
			if !tc.wantErr {
				if err != nil && strings.Contains(err.Error(), "--egress-allow") {
					t.Fatalf("unexpected egress error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != "--egress-allow is not supported for provider=hetzner" {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
