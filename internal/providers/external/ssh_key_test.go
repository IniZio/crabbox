package external

import (
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func sshKeyConfig(topLevel string) core.Config {
	cfg := testConfig()
	if topLevel != "" {
		cfg.SSHKey = topLevel
		core.MarkSSHKeyExplicit(&cfg)
	}
	return cfg
}

func connectionLease(t *testing.T, connectionKey string) protocolLease {
	t.Helper()
	ssh, err := lifecycleSSH(
		core.ExternalSSHConnectionConfig{User: "vm", Host: "vm.example.test", Key: connectionKey},
		lifecycleTemplateContext{values: map[string]string{}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return protocolLease{LeaseID: "cbx_abcdef123456", Name: "crabbox-blue-crab", SSH: &ssh}
}

func TestExternalTargetKeyPrecedence(t *testing.T) {
	providerLease := hostKeyLease("")
	providerLease.SSH.Key = "/keys/provider"
	cases := []struct {
		name  string
		lease protocolLease
		cfg   core.Config
		want  string
	}{
		{"top-level ssh.key only", connectionLease(t, ""), sshKeyConfig("/keys/top"), "/keys/top"},
		{"connection key wins over ssh.key", connectionLease(t, "/keys/connection"), sshKeyConfig("/keys/top"), "/keys/connection"},
		{"provider key wins over ssh.key", providerLease, sshKeyConfig("/keys/top"), "/keys/provider"},
		{"no key configured leaves ssh defaults", connectionLease(t, ""), sshKeyConfig(""), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lease, err := tc.lease.pinnedTarget(tc.cfg, true)
			if err != nil {
				t.Fatal(err)
			}
			if lease.SSH.Key != tc.want {
				t.Fatalf("key=%q, want %q", lease.SSH.Key, tc.want)
			}
		})
	}
}
