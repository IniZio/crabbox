package namespace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func hostKeyFixture(t *testing.T) (dir, key string) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	dir = t.TempDir()
	key = filepath.Join(dir, "box.key")
	return dir, key
}

func addHostKey(t *testing.T, khPath, host string) {
	t.Helper()
	keyPath := filepath.Join(t.TempDir(), "k")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", keyPath).CombinedOutput(); err != nil {
		t.Fatalf("keygen: %v %s", err, out)
	}
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(khPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(host + " " + string(pub)); err != nil {
		t.Fatal(err)
	}
}

func TestForgetNamespaceHostKeyRemovesStaleEntryOnly(t *testing.T) {
	dir, key := hostKeyFixture(t)
	kh := filepath.Join(dir, "known_hosts")
	addHostKey(t, kh, "box-a.devbox.namespace")
	addHostKey(t, kh, "box-b.devbox.namespace")
	err := forgetNamespaceHostKey(core.SSHTarget{Host: "box-a.devbox.namespace", Key: key})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(kh)
	if strings.Contains(string(data), "box-a.devbox.namespace") || !strings.Contains(string(data), "box-b.devbox.namespace") {
		t.Fatalf("known_hosts = %q", data)
	}
	if _, err := os.Stat(kh + ".old"); err == nil {
		t.Fatal(".old backup left behind")
	}
}

func TestForgetNamespaceHostKeyFreshHostLeavesFileUntouched(t *testing.T) {
	dir, key := hostKeyFixture(t)
	kh := filepath.Join(dir, "known_hosts")
	addHostKey(t, kh, "other.devbox.namespace")
	before, _ := os.ReadFile(kh)
	if err := forgetNamespaceHostKey(core.SSHTarget{Host: "fresh.devbox.namespace", Key: key}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(kh)
	if string(before) != string(after) {
		t.Fatalf("known_hosts changed: %q -> %q", before, after)
	}
	// Missing known_hosts file is a no-op.
	if err := forgetNamespaceHostKey(core.SSHTarget{Host: "x", Key: filepath.Join(t.TempDir(), "k")}); err != nil {
		t.Fatal(err)
	}
}
