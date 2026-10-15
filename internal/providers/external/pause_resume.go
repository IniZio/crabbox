package external

import (
	"context"
	"fmt"
	"strings"

	core "github.com/openclaw/crabbox/internal/cli"
)

const stderrTailMaxBytes = 2048

var _ core.PausableBackend = (*leaseBackend)(nil)

// Pause asks an opted-in adapter to stop the lease's compute while keeping its state.
func (b *leaseBackend) Pause(ctx context.Context, req core.PauseRequest) error {
	return b.pauseResume(ctx, "pause", "paused", req.ID)
}

// Resume asks an opted-in adapter to start a paused lease again.
func (b *leaseBackend) Resume(ctx context.Context, req core.ResumeRequest) error {
	return b.pauseResume(ctx, "resume", "resumed", req.ID)
}

func (b *leaseBackend) pauseResume(ctx context.Context, op, done, id string) error {
	if !b.cfg.External.Capabilities.PauseResume || lifecycleConfigured(b.cfg.External) {
		return core.Exit(2, "provider=%s does not support %s", providerName, op)
	}
	// Release-only resolve skips SSH readiness, routing persistence and claim
	// binding, so it also answers for a stopped (paused) lease.
	lease, err := b.Resolve(ctx, core.ResolveRequest{ID: id, ReleaseOnly: true})
	if err != nil {
		return err
	}
	response, err := b.invoke(ctx, protocolRequest{Operation: op, Lease: leaseForProtocol(lease)})
	if err != nil {
		return err
	}
	// A resumed instance may have moved host, so a fresh host key replaces the pin.
	if op == "resume" && response.Lease != nil {
		resumed := *response.Lease
		if resumed.LeaseID == "" {
			resumed.LeaseID = lease.LeaseID
		}
		if _, err := resumed.pinnedTarget(b.cfg, true); err != nil {
			return err
		}
	}
	if b.rt.Stderr != nil {
		fmt.Fprintf(b.rt.Stderr, "%s lease=%s\n", done, lease.LeaseID)
	}
	return nil
}

func stderrTail(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if len(stderr) > stderrTailMaxBytes {
		stderr = strings.TrimSpace(stderr[len(stderr)-stderrTailMaxBytes:])
	}
	return stderr
}
