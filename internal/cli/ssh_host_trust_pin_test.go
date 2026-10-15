package cli

import (
	"strings"
	"testing"
)

func TestAuthoritativeLeaseTargetVerificationArgsAreStrictAndIsolated(t *testing.T) {
	alias, err := LeaseHostKeyAlias("cbx_abcdef123456")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(sshHostKeyVerificationArgs(SSHTarget{
		KnownHostsFile:          "/state/lease/known_hosts",
		AuthoritativeKnownHosts: true,
		HostKeyAlias:            alias,
	}), " ")
	for _, want := range []string{"StrictHostKeyChecking=yes", "UserKnownHostsFile=/state/lease/known_hosts", "GlobalKnownHostsFile=none", "HostKeyAlias=" + alias} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %q in %s", want, args)
		}
	}
}
