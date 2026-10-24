package external

import (
	"context"
	"path"
	"strings"
	"unicode"

	core "github.com/openclaw/crabbox/internal/cli"
)

const loginHomeCommand = `printf %s "$HOME"`

// resolveRelativeWorkRoot joins a relative work root to the login home the
// lease's own SSH session reports. It never falls back to a default root,
// because a wrong root would put the Box's work on the wrong disk.
func (b *leaseBackend) resolveRelativeWorkRoot(ctx context.Context, lease *core.LeaseTarget) error {
	root := strings.TrimSpace(b.cfg.WorkRoot)
	if root == "" || strings.HasPrefix(path.Clean(root), "/") ||
		(b.cfg.TargetOS == core.TargetWindows && b.cfg.WindowsMode == core.WindowsModeNormal) {
		return nil
	}
	home, err := core.RunSSHOutputBounded(ctx, lease.SSH, loginHomeCommand, 4096)
	if err != nil {
		return core.Exit(5, "read login home of %s@%s to resolve external.workRoot %q: %v", lease.SSH.User, lease.SSH.Host, root, err)
	}
	if !strings.HasPrefix(home, "/") || strings.IndexFunc(home, unicode.IsControl) >= 0 {
		return core.Exit(5, "login home %q of %s@%s is not an absolute path; cannot resolve external.workRoot %q", home, lease.SSH.User, lease.SSH.Host, root)
	}
	lease.Server.Labels["work_root"] = path.Join(home, root)
	return nil
}
