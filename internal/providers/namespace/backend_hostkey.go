package namespace

import (
	"os"
	"os/exec"
	"path/filepath"

	core "github.com/openclaw/crabbox/internal/cli"
)

// forgetNamespaceHostKey removes target.Host from the known_hosts file the
// crabbox SSH client uses for target (same resolution as cli.knownHostsFile).
func forgetNamespaceHostKey(target core.SSHTarget) error {
	path := target.KnownHostsFile
	if path == "" && target.Key != "" {
		path = filepath.Join(filepath.Dir(target.Key), "known_hosts")
	}
	if path == "" || target.Host == "" {
		return nil
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	if out, err := exec.Command("ssh-keygen", "-R", target.Host, "-f", path).CombinedOutput(); err != nil {
		return &hostKeyError{err: err, out: string(out)}
	}
	_ = os.Remove(path + ".old")
	return nil
}

type hostKeyError struct {
	err error
	out string
}

func (e *hostKeyError) Error() string { return e.err.Error() + ": " + e.out }
