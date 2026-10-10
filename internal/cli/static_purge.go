package cli

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// StaticWorkspacePurgeCommand returns a POSIX command that removes one static
// lease's remote workdir, workspace-owner gate and Actions stop marker. It
// refuses an id that is empty or could escape workRoot, and a workRoot that is
// not absolute.
func StaticWorkspacePurgeCommand(workRoot, leaseID string) (string, error) {
	if leaseID == "" || leaseID == "." || strings.ContainsAny(leaseID, `/\`) || strings.Contains(leaseID, "..") {
		return "", fmt.Errorf("refusing to purge workspace for lease id %q", leaseID)
	}
	root := path.Clean(strings.TrimSpace(workRoot))
	if !path.IsAbs(root) || root == "/" {
		return "", fmt.Errorf("refusing to purge workspace under work root %q", workRoot)
	}
	dir := path.Join(root, leaseID)
	if !strings.HasPrefix(dir, root+"/") || dir == root+"/" {
		return "", fmt.Errorf("refusing to purge %q: not strictly under %q", dir, root)
	}
	gate := ".crabbox/workspace-owners/" + workspaceOwnerKey(leaseID) + ".gate"
	return "rm -rf -- " + shellQuote(dir) +
		" && rm -f -- \"$HOME\"/" + shellQuote(gate) + " \"$HOME\"/" + shellQuote(actionsHydrationStopPath(leaseID)), nil
}

// PurgeStaticWorkspace runs StaticWorkspacePurgeCommand on target.
func PurgeStaticWorkspace(ctx context.Context, target SSHTarget, workRoot, leaseID string) error {
	cmd, err := StaticWorkspacePurgeCommand(workRoot, leaseID)
	if err != nil {
		return err
	}
	return RunSSHQuiet(ctx, target, cmd)
}
