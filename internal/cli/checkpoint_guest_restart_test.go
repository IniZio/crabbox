//go:build darwin || linux

package cli

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

type restartingCheckpointProvider struct {
	heartbeatDirectProvider
}

func (restartingCheckpointProvider) Spec() ProviderSpec {
	s := heartbeatDirectProvider{}.Spec()
	s.Name = "restarting-checkpoint-test"
	return s
}

func init() { RegisterProvider(restartingCheckpointProvider{}) }

// restartingCheckpointMaster is the control socket a guest restart drops, and
// restartingCheckpointErr is what the provider reports after restarting.
var restartingCheckpointMaster string
var restartingCheckpointErr error

func (restartingCheckpointProvider) NativeCheckpointWorkdir(NativeCheckpointWorkdirRequest) string {
	return ""
}

func (restartingCheckpointProvider) CreateNativeCheckpoint(ctx context.Context, req NativeCheckpointCreateRequest) (NativeCheckpointCreateResult, error) {
	// The provider restores the guest from its snapshot, which drops every
	// connection crabbox still holds to it.
	out, err := exec.CommandContext(ctx, directSSHExecutable(), "-S", restartingCheckpointMaster, "-O", "check", "--", "localhost").CombinedOutput()
	if err == nil {
		value := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(string(out)), "Master running (pid="), ")")
		if pid, convErr := strconv.Atoi(value); convErr == nil && pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	if restartingCheckpointErr != nil {
		return NativeCheckpointCreateResult{}, restartingCheckpointErr
	}
	return NativeCheckpointCreateResult{
		Image:              NativeCheckpointImage{ID: req.CheckpointID, State: "available", Provider: "restarting-checkpoint-test"},
		CaptureEstablished: true,
	}, nil
}

func (restartingCheckpointProvider) VerifyNativeCheckpoint(context.Context, NativeCheckpointResourceRequest) (NativeCheckpointVerifyResult, error) {
	return NativeCheckpointVerifyResult{}, nil
}

func (restartingCheckpointProvider) DeleteNativeCheckpoint(context.Context, NativeCheckpointResourceRequest) error {
	return nil
}

func createCheckpointAcrossGuestRestart(t *testing.T, providerErr error) (*serveRelayControls, SSHTarget, context.Context, error) {
	t.Helper()
	_, target := pinnedProxyFixture(t)
	ctx, controls := withSSHControls(t)
	master := pinnedProxyTarget(t, ctx, target)
	if master.ownerControlPath == "" {
		t.Fatal("lease SSH master did not start")
	}
	restartingCheckpointMaster, restartingCheckpointErr = master.ownerControlPath, providerErr
	cfg := defaultConfig()
	cfg.Provider = "restarting-checkpoint-test"
	_, _, err := (App{}).createNativeCheckpointRequest(ctx, NativeCheckpointCreateRequest{
		Config: cfg, Server: Server{Provider: cfg.Provider}, Target: target,
		CheckpointID: "chk_restart", LeaseID: invocationMasterLeaseID,
	})
	return controls, target, ctx, err
}

func TestCheckpointThatRestartsTheGuestSucceeds(t *testing.T) {
	controls, target, ctx, err := createCheckpointAcrossGuestRestart(t, nil)
	if err != nil {
		t.Fatalf("checkpoint the provider wrote failed: %v", err)
	}
	if err := runSSHQuiet(ctx, target, "exit 0"); err != nil {
		t.Fatalf("lease unreachable after the checkpoint: %v", err)
	}
	if err := controls.closeLease(""); err != nil {
		t.Fatalf("command exit reported the restart as a failure: %v", err)
	}
}

func TestCheckpointProviderFailureStillFails(t *testing.T) {
	failure := errors.New("snapshot failed")
	_, _, _, err := createCheckpointAcrossGuestRestart(t, failure)
	if !errors.Is(err, failure) {
		t.Fatalf("err=%v, want the provider failure", err)
	}
}
