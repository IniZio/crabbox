package azuresandbox

import (
	"context"
	"fmt"
	"io"
	"time"

	core "github.com/openclaw/crabbox/internal/cli"
	"github.com/openclaw/crabbox/internal/providers/shared"
)

func (b *backend) Warmup(ctx context.Context, req core.WarmupRequest) error {
	return b.WarmupFixed(ctx, core.FixedWarmupRequest{WarmupRequest: req, RequestedLeaseID: core.NewLeaseID()})
}

func (b *backend) WarmupFixed(ctx context.Context, req core.FixedWarmupRequest) error {
	started := time.Now()
	lease, err := b.acquire(ctx, req)
	if err != nil {
		return err
	}
	claim, err := b.claim(lease.LeaseID)
	if err != nil {
		return err
	}
	if req.OnAcquired != nil {
		if err := req.OnAcquired(core.FixedAcquisitionReceipt{LeaseID: lease.LeaseID, Slug: claim.Slug, Provider: providerName, ResourceID: lease.Server.CloudID}); err != nil {
			return err
		}
	}
	if req.BeforeComplete != nil {
		req.BeforeComplete()
	}
	return shared.CompleteWarmup(b.rt, req.TimingJSON, shared.WarmupCompletion{Provider: providerName, LeaseID: lease.LeaseID, Slug: claim.Slug, Total: time.Since(started)})
}

func (b *backend) claim(id string) (core.LeaseClaim, error) {
	claim, exists, err := core.ReadLeaseClaimWithPresence(id)
	if err != nil {
		return claim, err
	}
	if !exists || !leaseKind.IsFixedClaim(claim) || claim.ProviderScope != (Provider{}).ClaimScope(b.cfg) {
		return claim, fmt.Errorf("ACA Sandbox requires an exact locally owned lease in the configured group")
	}
	return claim, nil
}

// A heartbeat or configuration change cannot extend the original allocation.
// Teardown deliberately does not depend on this execution-admission check.
func executionDeadline(claim core.LeaseClaim) (time.Time, error) {
	i := claim.FixedCreateIntent
	if i == nil || i.State != "acquired" || claim.CloudID == "" {
		return time.Time{}, fmt.Errorf("ACA Sandbox has no completed allocation")
	}
	deadline, err := time.Parse(time.RFC3339Nano, i.Attempt["expires_at"])
	if err != nil || !deadline.After(time.Now()) {
		return time.Time{}, fmt.Errorf("ACA Sandbox allocation expired; release the lease")
	}
	return deadline, nil
}

func (b *backend) List(ctx context.Context, _ core.ListRequest) ([]core.LeaseView, error) {
	claims, err := core.ListLeaseClaims()
	if err != nil {
		return nil, err
	}
	var result []core.LeaseView
	for _, claim := range claims {
		if !leaseKind.IsFixedClaim(claim) || claim.ProviderScope != (Provider{}).ClaimScope(b.cfg) || claim.FixedCreateIntent.State == "released" {
			continue
		}
		s, err := b.inspect(ctx, claim)
		if err != nil {
			return nil, err
		}
		result = append(result, core.Server{Provider: providerName, CloudID: s.ID, ImmutableID: s.ID, Name: claim.Slug, Status: s.State, Labels: s.Labels})
	}
	return result, nil
}

func (b *backend) inspect(ctx context.Context, claim core.LeaseClaim) (sandbox, error) {
	observed, err := core.InspectFixedResource(ctx, leaseKind, claim, core.FixedLeaseOperations[sandbox]{ObserveExact: b.observe})
	if err != nil {
		return sandbox{}, err
	}
	if len(observed.Candidates) != 1 {
		return sandbox{}, fmt.Errorf("ACA Sandbox resource is missing or uncertain; reconcile this lease")
	}
	return observed.Candidates[0], nil
}

func (b *backend) Status(ctx context.Context, req core.StatusRequest) (core.StatusView, error) {
	claim, err := b.claim(req.ID)
	if err != nil {
		return core.StatusView{}, err
	}
	view := core.StatusView{ID: claim.LeaseID, Slug: claim.Slug, Provider: providerName, TargetOS: core.TargetLinux, Network: core.NetworkPublic, ServerID: claim.CloudID}
	if claim.FixedCreateIntent.State == "released" {
		view.State = "deleted"
		return view, nil
	}
	s, err := b.inspect(ctx, claim)
	if err != nil {
		return view, err
	}
	view.State, view.ServerID, view.Ready, view.Labels = s.State, s.ID, s.State == "Running", s.Labels
	return view, nil
}

func (b *backend) Stop(ctx context.Context, req core.StopRequest) error {
	return b.stop(ctx, req, "")
}

func (b *backend) StopForRepository(ctx context.Context, req core.StopRequest, root string) error {
	return b.stop(ctx, req, root)
}

func (b *backend) stop(ctx context.Context, req core.StopRequest, root string) error {
	claim, err := b.claim(req.ID)
	if err != nil {
		return err
	}
	c, err := b.connect()
	if err != nil {
		return err
	}
	return core.DeleteFixedResource(ctx, leaseKind, claim, core.FixedLeaseOperations[sandbox]{
		Release:      &core.FixedReleasePolicy{RepoRoot: root, SkipTerminalObservation: true},
		ObserveExact: b.observe,
		DeleteExact: func(ctx context.Context, _ *core.FixedTransaction, s sandbox) error {
			if err := c.Delete(ctx, s.ID); err != nil && !isMissing(err) {
				return err
			}
			for {
				_, err := c.Get(ctx, s.ID)
				if isMissing(err) {
					return nil
				}
				if err != nil {
					return err
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
			}
		},
	})
}

func (b *backend) Run(ctx context.Context, req core.RunRequest) (core.RunResult, error) {
	if req.ID == "" || !req.Keep || !req.NoSync {
		return core.RunResult{}, fmt.Errorf("ACA Sandbox execution requires a kept lease and --no-sync; workspace preparation is caller-owned")
	}
	claim, err := b.claim(req.ID)
	if err != nil {
		return core.RunResult{}, err
	}
	if err := core.CheckLeaseClaimRepositoryOwner(req.ID, claim, req.Repo.Root, false); err != nil {
		return core.RunResult{}, err
	}
	deadline, err := executionDeadline(claim)
	if err != nil {
		return core.RunResult{}, err
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	c, err := b.connect()
	if err != nil {
		return core.RunResult{}, err
	}
	var result core.RunResult
	err = core.WithLeaseClaimUnchangedShared(ctx, claim.LeaseID, claim, func() error {
		s, err := b.inspect(ctx, claim)
		if err != nil {
			return err
		}
		if s.State != "Running" {
			return fmt.Errorf("ACA Sandbox execution requires a running lease")
		}
		var runErr error
		result, runErr = shared.RunDelegatedSandbox(ctx, req, shared.DelegatedSandboxLifecycle{
			Provider: providerName, Runtime: b.rt, Workdir: "/workspace", TTL: b.cfg.TTL, IdleTimeout: b.cfg.IdleTimeout,
			Resolve: func(context.Context) (shared.DelegatedSandbox, error) {
				return shared.DelegatedSandbox{LeaseID: claim.LeaseID, Slug: claim.Slug,
					CleanupCommand: "crabbox stop --provider azure-sandbox --id " + core.ShellQuote(claim.LeaseID)}, nil
			},
			Workspace: func() shared.SandboxWorkspace {
				return shared.WorkspaceOperations{EnsureFunc: func(context.Context) error { return nil }}
			},
			Command: func(context.Context) (shared.DelegatedSandboxCommand, error) {
				commandArgs, shellMode := req.Command, req.ShellMode
				literalArgs := req.CommandLiteralArgs
				if req.Script != nil {
					commandArgs = append([]string{"bash", "-c", string(req.Script.Data), "crabbox-script"}, req.Command...)
					shellMode = false
					literalArgs = nil
				}
				intent, err := core.ParseCommandIntent(commandArgs, shellMode, literalArgs)
				if err != nil {
					return shared.DelegatedSandboxCommand{}, err
				}
				command := shared.ShellWorkspaceCommand("/workspace", req.Env, intent, "bash", "-c")
				return shared.DelegatedSandboxCommand{Text: intent.ShellScript(), Run: func(ctx context.Context, stdout, stderr io.Writer) (int, error) {
					out, err := c.Exec(ctx, claim.CloudID, command, "")
					if err != nil {
						return 1, err
					}
					if _, err := io.WriteString(stdout, out.Stdout); err != nil {
						return 1, err
					}
					if _, err := io.WriteString(stderr, out.Stderr); err != nil {
						return 1, err
					}
					return *out.ExitCode, nil
				}}, nil
			},
		})
		return runErr
	})
	return result, err
}

func (b *backend) Heartbeat(ctx context.Context, req core.LeaseHeartbeatRequest) (core.LeaseHeartbeatResult, error) {
	claim, err := b.claim(req.ID)
	if err != nil {
		return core.LeaseHeartbeatResult{}, err
	}
	deadline, err := executionDeadline(claim)
	if err != nil {
		return core.LeaseHeartbeatResult{}, err
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	err = core.WithLeaseClaimUnchangedShared(ctx, claim.LeaseID, claim, func() error {
		s, err := b.inspect(ctx, claim)
		if err != nil {
			return err
		}
		if s.State != "Running" {
			return fmt.Errorf("ACA Sandbox heartbeat requires a running lease")
		}
		out, err := b.api.Exec(ctx, s.ID, "true", "")
		if err != nil {
			return err
		}
		if *out.ExitCode != 0 {
			return fmt.Errorf("ACA Sandbox heartbeat failed")
		}
		return nil
	})
	return core.LeaseHeartbeatResult{LeaseID: claim.LeaseID, Slug: claim.Slug, State: "Running", LastTouchedAt: time.Now()}, err
}
