package external

import (
	"context"
	"io"
	"strings"

	core "github.com/openclaw/crabbox/internal/cli"
)

var (
	_ core.NativeCheckpointProvider          = Provider{}
	_ core.NativeCheckpointLifecycleProvider = Provider{}
	_ core.NativeCheckpointForkProvider      = Provider{}
)

func checkpointsSupported(cfg core.Config) error {
	if !cfg.External.Capabilities.Checkpoints || lifecycleConfigured(cfg.External) {
		return core.Exit(2, "provider=%s does not support checkpoints; an adapter that implements checkpointCreate, checkpointFork and checkpointDelete opts in with external.capabilities.checkpoints in trusted user config", providerName)
	}
	return nil
}

// NativeCheckpointCapability is reported without the opt-in so that native
// create, fork and delete reach the operations, which explain the missing
// capability instead of a generic unsupported-mode error.
func (Provider) NativeCheckpointCapability(core.NativeCheckpointRequest) (core.NativeCheckpointCapability, bool) {
	return core.NativeCheckpointCapability{Kind: core.CheckpointKindExternal, Direct: true}, true
}

func (Provider) NativeCheckpointWorkdir(req core.NativeCheckpointWorkdirRequest) string {
	if req.Override != "" {
		return req.Override
	}
	cfg := req.Config
	if root := req.Server.Labels["work_root"]; root != "" {
		cfg.WorkRoot = root
	}
	return core.RemoteJoin(cfg, req.LeaseID, req.RepoName)
}

func (p Provider) CreateNativeCheckpoint(ctx context.Context, req core.NativeCheckpointCreateRequest) (core.NativeCheckpointCreateResult, error) {
	var result core.NativeCheckpointCreateResult
	backend, err := p.checkpointBackend(req.Config, req.Stderr)
	if err != nil {
		return result, core.NativeCheckpointNotSubmittedError{Cause: err}
	}
	response, err := backend.invoke(ctx, protocolRequest{Operation: "checkpointCreate", LeaseID: req.LeaseID, CheckpointID: req.CheckpointID, Name: req.Name})
	if err != nil {
		return result, err
	}
	if id := strings.TrimSpace(response.CheckpointID); id != "" && id != req.CheckpointID {
		return result, core.Exit(5, "external provider created checkpoint %s, want %s", id, req.CheckpointID)
	}
	result.Image = core.NativeCheckpointImage{
		ID: req.CheckpointID, Name: req.Name, State: "available", Provider: providerName,
		Kind: core.CheckpointKindExternal, ResourceID: req.CheckpointID, Bytes: response.Bytes, Direct: true,
		ParentCheckpointID: strings.TrimSpace(response.ParentCheckpointID), UniqueBytes: response.UniqueBytes, ApparentBytes: response.ApparentBytes,
	}
	result.CaptureEstablished = true
	return result, nil
}

// VerifyNativeCheckpoint has no protocol lookup; a missing checkpoint surfaces
// as "checkpoint not found" on fork.
func (Provider) VerifyNativeCheckpoint(context.Context, core.NativeCheckpointResourceRequest) (core.NativeCheckpointVerifyResult, error) {
	return core.NativeCheckpointVerifyResult{ProviderState: "unverified_ref", NextAction: "fork_or_delete"}, nil
}

func (p Provider) DeleteNativeCheckpoint(ctx context.Context, req core.NativeCheckpointResourceRequest) error {
	if req.Image.Kind != core.CheckpointKindExternal || strings.TrimSpace(req.Image.ID) == "" {
		return core.Exit(2, "%s checkpoint record has no checkpoint id", providerName)
	}
	if req.LoadConfig == nil {
		return core.Exit(2, "%s checkpoint delete requires provider configuration", providerName)
	}
	cfg, err := req.LoadConfig()
	if err != nil {
		return err
	}
	backend, err := p.checkpointBackend(cfg, nil)
	if err != nil {
		return err
	}
	_, err = backend.invoke(ctx, protocolRequest{Operation: "checkpointDelete", CheckpointID: req.Image.ID})
	return err
}

func (Provider) ApplyNativeCheckpointForkConfig(req core.NativeCheckpointForkRequest) error {
	if req.Record.Kind != core.CheckpointKindExternal || strings.TrimSpace(req.Record.ImageID) == "" {
		return core.Exit(2, "%s can only fork %s checkpoints", providerName, core.CheckpointKindExternal)
	}
	return checkpointsSupported(*req.Config)
}

func (p Provider) checkpointBackend(cfg core.Config, stderr io.Writer) (*leaseBackend, error) {
	if err := checkpointsSupported(cfg); err != nil {
		return nil, err
	}
	backend, err := p.Configure(cfg, core.RuntimeForProviderOperation(stderr))
	if err != nil {
		return nil, err
	}
	return backend.(*leaseBackend), nil
}

// acquireOperation turns an acquire from a checkpoint into checkpointFork,
// which the adapter answers like acquire.
func acquireOperation(cfg core.Config, source *core.NativeCheckpointForkRecord) (string, string, error) {
	if source == nil {
		return "acquire", "", nil
	}
	if err := checkpointsSupported(cfg); err != nil {
		return "", "", err
	}
	if source.Kind != core.CheckpointKindExternal || strings.TrimSpace(source.ImageID) == "" {
		return "", "", core.Exit(2, "%s can only fork %s checkpoints", providerName, core.CheckpointKindExternal)
	}
	return "checkpointFork", source.ImageID, nil
}
