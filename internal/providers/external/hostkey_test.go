package external

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"strings"
	"testing"

	xssh "golang.org/x/crypto/ssh"
)

func testHostKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := xssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(xssh.MarshalAuthorizedKey(key)))
}

func hostKeyLease(hostKey string) protocolLease {
	return protocolLease{
		LeaseID: "cbx_abcdef123456",
		Slug:    "blue-crab",
		Name:    "crabbox-blue-crab",
		SSH:     &protocolSSH{User: "vm", Host: "vm.example.test", Port: "22", HostKey: hostKey},
	}
}

func readKnownHosts(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPinnedHostKeyIsStrictAndExclusive(t *testing.T) {
	isolateCrabboxState(t)
	key := testHostKey(t)
	lease, err := hostKeyLease(key).pinnedTarget(testConfig(), true)
	if err != nil {
		t.Fatal(err)
	}
	ssh := lease.SSH
	if ssh.SSHHostKey != key || !ssh.AuthoritativeKnownHosts || ssh.HostKeyAlias == "" || ssh.KnownHostsFile == "" {
		t.Fatalf("target not pinned: %#v", ssh)
	}
	got := readKnownHosts(t, ssh.KnownHostsFile)
	if strings.Count(got, "\n") != 1 || !strings.Contains(got, strings.Fields(key)[1]) {
		t.Fatalf("known_hosts=%q", got)
	}
}

func TestPinnedHostKeyChangeReplacesOldKey(t *testing.T) {
	isolateCrabboxState(t)
	oldKey, newKey := testHostKey(t), testHostKey(t)
	first, err := hostKeyLease(oldKey).pinnedTarget(testConfig(), true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := hostKeyLease(newKey).pinnedTarget(testConfig(), true)
	if err != nil {
		t.Fatal(err)
	}
	if first.SSH.KnownHostsFile != second.SSH.KnownHostsFile {
		t.Fatalf("known_hosts path changed: %q vs %q", first.SSH.KnownHostsFile, second.SSH.KnownHostsFile)
	}
	got := readKnownHosts(t, second.SSH.KnownHostsFile)
	if strings.Contains(got, strings.Fields(oldKey)[1]) || !strings.Contains(got, strings.Fields(newKey)[1]) || strings.Count(got, "\n") != 1 {
		t.Fatalf("known_hosts=%q", got)
	}
}

func TestMissingHostKeyKeepsExistingPinStrict(t *testing.T) {
	isolateCrabboxState(t)
	key := testHostKey(t)
	pinned, err := hostKeyLease(key).pinnedTarget(testConfig(), true)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := hostKeyLease("").pinnedTarget(testConfig(), true)
	if err != nil {
		t.Fatal(err)
	}
	ssh := lease.SSH
	if !ssh.AuthoritativeKnownHosts || ssh.KnownHostsFile != pinned.SSH.KnownHostsFile || ssh.HostKeyAlias != pinned.SSH.HostKeyAlias {
		t.Fatalf("pin dropped: %#v", ssh)
	}
	if got := readKnownHosts(t, ssh.KnownHostsFile); !strings.Contains(got, strings.Fields(key)[1]) {
		t.Fatalf("old pin lost: %q", got)
	}
}

func TestNoHostKeyEverLeavesTargetUnchanged(t *testing.T) {
	isolateCrabboxState(t)
	want := hostKeyLease("").target(testConfig(), true)
	got, err := hostKeyLease("").pinnedTarget(testConfig(), true)
	if err != nil {
		t.Fatal(err)
	}
	if got.SSH.SSHHostKey != "" || got.SSH.AuthoritativeKnownHosts || got.SSH.KnownHostsFile != "" || got.SSH.HostKeyAlias != "" {
		t.Fatalf("unexpected pin: %#v", got.SSH)
	}
	if got.SSH.User != want.SSH.User || got.SSH.Host != want.SSH.Host || got.SSH.Port != want.SSH.Port {
		t.Fatalf("target changed: %#v vs %#v", got.SSH, want.SSH)
	}
}

func TestMalformedHostKeyFailsClosed(t *testing.T) {
	isolateCrabboxState(t)
	if _, err := hostKeyLease("not-a-key").pinnedTarget(testConfig(), true); err == nil {
		t.Fatal("expected error for malformed host key")
	}
}
