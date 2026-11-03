package external

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

const testCheckpointID = "chk_0123456789abcdef"

// checkpointAdapter writes a shell adapter that logs each request and answers
// every operation with body.
func checkpointAdapter(t *testing.T, optIn bool, body string) (core.Config, string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	isolateCrabboxState(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "requests.log")
	script := filepath.Join(dir, "adapter.sh")
	content := "#!/bin/sh\nreq=$(cat)\nprintf '%s\\n' \"$req\" >> " + logPath + "\n" + body + "\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.External.Command = script
	cfg.External.Args = nil
	cfg.External.Capabilities.Checkpoints = optIn
	return cfg, logPath
}

func externalCheckpointImage() core.NativeCheckpointImage {
	return core.NativeCheckpointImage{ID: testCheckpointID, Provider: providerName, Kind: core.CheckpointKindExternal, Direct: true}
}

func TestCheckpointOperationsRequireCapability(t *testing.T) {
	cfg, logPath := checkpointAdapter(t, false, `printf '%s' '{"protocolVersion":1}'`)
	load := func() (core.Config, error) { return cfg, nil }
	backend := &leaseBackend{cfg: cfg, rt: core.Runtime{Stderr: &bytes.Buffer{}, Exec: scriptRunner{}}}
	record := core.NativeCheckpointForkRecord{Kind: core.CheckpointKindExternal, ImageID: testCheckpointID}
	for name, run := range map[string]func() error{
		"create": func() error {
			_, err := (Provider{}).CreateNativeCheckpoint(context.Background(), core.NativeCheckpointCreateRequest{
				Config: cfg, CheckpointID: testCheckpointID, LeaseID: "cbx_abcdef123456", Name: "base",
			})
			return err
		},
		"delete": func() error {
			return (Provider{}).DeleteNativeCheckpoint(context.Background(), core.NativeCheckpointResourceRequest{LoadConfig: load, Image: externalCheckpointImage()})
		},
		"fork config": func() error {
			return (Provider{}).ApplyNativeCheckpointForkConfig(core.NativeCheckpointForkRequest{Config: &cfg, Record: record})
		},
		"fork acquire": func() error {
			_, err := backend.Acquire(context.Background(), core.AcquireRequest{CheckpointSource: &record})
			return err
		},
	} {
		err := run()
		if err == nil || !strings.Contains(err.Error(), "provider=external does not support checkpoints") {
			t.Fatalf("%s err=%v, want unsupported checkpoints", name, err)
		}
	}
	if reqs := readRequests(t, logPath); len(reqs) != 0 {
		t.Fatalf("adapter invoked without opt-in: %#v", reqs)
	}
}

func TestCheckpointCreateSendsLeaseCheckpointAndName(t *testing.T) {
	cfg, logPath := checkpointAdapter(t, true, `printf '%s' '{"protocolVersion":1,"checkpointId":"`+testCheckpointID+`","bytes":4096}'`)
	result, err := (Provider{}).CreateNativeCheckpoint(context.Background(), core.NativeCheckpointCreateRequest{
		Config: cfg, CheckpointID: testCheckpointID, LeaseID: "cbx_abcdef123456", Name: "after-clone",
	})
	if err != nil {
		t.Fatal(err)
	}
	reqs := readRequests(t, logPath)
	if len(reqs) != 1 {
		t.Fatalf("requests=%#v", reqs)
	}
	req := reqs[0]
	if req.Operation != "checkpointCreate" || req.LeaseID != "cbx_abcdef123456" || req.CheckpointID != testCheckpointID || req.Name != "after-clone" || req.ProtocolVersion != protocolVersion {
		t.Fatalf("request=%#v", req)
	}
	image := result.Image
	if image.ID != testCheckpointID || image.Kind != core.CheckpointKindExternal || image.Provider != providerName || image.Bytes != 4096 || image.State != "available" || !image.Direct {
		t.Fatalf("image=%#v", image)
	}
	if !result.CaptureEstablished {
		t.Fatal("capture not established")
	}
}

func TestCheckpointCreateRejectsChangedIdentity(t *testing.T) {
	cfg, _ := checkpointAdapter(t, true, `printf '%s' '{"protocolVersion":1,"checkpointId":"chk_ffffffffffffffff","bytes":1}'`)
	_, err := (Provider{}).CreateNativeCheckpoint(context.Background(), core.NativeCheckpointCreateRequest{
		Config: cfg, CheckpointID: testCheckpointID, LeaseID: "cbx_abcdef123456",
	})
	if err == nil || !strings.Contains(err.Error(), "chk_ffffffffffffffff") {
		t.Fatalf("err=%v", err)
	}
}

func TestCheckpointDeleteSendsCheckpointID(t *testing.T) {
	cfg, logPath := checkpointAdapter(t, true, `printf '%s' '{"protocolVersion":1}'`)
	load := func() (core.Config, error) { return cfg, nil }
	if err := (Provider{}).DeleteNativeCheckpoint(context.Background(), core.NativeCheckpointResourceRequest{LoadConfig: load, Image: externalCheckpointImage()}); err != nil {
		t.Fatal(err)
	}
	reqs := readRequests(t, logPath)
	if len(reqs) != 1 || reqs[0].Operation != "checkpointDelete" || reqs[0].CheckpointID != testCheckpointID {
		t.Fatalf("requests=%#v", reqs)
	}
}

func TestCheckpointForkSendsAcquireRequestWithCheckpointID(t *testing.T) {
	cfg, logPath := checkpointAdapter(t, true, `echo "checkpoint not found: `+testCheckpointID+`" >&2; exit 3`)
	backend := &leaseBackend{cfg: cfg, rt: core.Runtime{Stderr: &bytes.Buffer{}, Exec: scriptRunner{}}}
	record := core.NativeCheckpointForkRecord{Kind: core.CheckpointKindExternal, ImageID: testCheckpointID}
	_, err := backend.Acquire(context.Background(), core.AcquireRequest{CheckpointSource: &record, RequestedSlug: "fork-1", Keep: true})
	if err == nil || !strings.Contains(err.Error(), "checkpoint not found") {
		t.Fatalf("err=%v", err)
	}
	reqs := readRequests(t, logPath)
	if len(reqs) != 1 {
		t.Fatalf("requests=%#v", reqs)
	}
	req := reqs[0]
	if req.Operation != "checkpointFork" || req.CheckpointID != testCheckpointID || req.Desired == nil || req.Desired.Slug != "fork-1" || !strings.HasPrefix(req.Desired.LeaseID, "cbx_") || !req.Keep {
		t.Fatalf("request=%#v desired=%#v", req, req.Desired)
	}
}

func TestCheckpointForkConfigRejectsOtherKinds(t *testing.T) {
	cfg := testConfig()
	cfg.External.Capabilities.Checkpoints = true
	err := (Provider{}).ApplyNativeCheckpointForkConfig(core.NativeCheckpointForkRequest{Config: &cfg, Record: core.NativeCheckpointForkRecord{Kind: core.CheckpointKindIncus, ImageID: "x"}})
	if err == nil {
		t.Fatal("accepted an Incus checkpoint")
	}
}

func TestCheckpointCapabilityTracksOptIn(t *testing.T) {
	cfg := testConfig()
	server := core.Server{Provider: providerName, CloudID: "box-1"}
	capability, ok := (Provider{}).NativeCheckpointCapability(core.NativeCheckpointRequest{Config: cfg, Server: server})
	if !ok || capability.Kind != core.CheckpointKindExternal || !capability.Direct {
		t.Fatalf("capability=%#v ok=%v", capability, ok)
	}
	features := (Provider{}).ConfiguredFeatures(cfg)
	for _, feature := range []core.Feature{core.FeatureCheckpoint, core.FeatureFork, core.FeatureSnapshot} {
		if features.Has(feature) {
			t.Fatalf("%s listed without opt-in: %v", feature, features)
		}
	}
	cfg.External.Capabilities.Checkpoints = true
	features = (Provider{}).ConfiguredFeatures(cfg)
	for _, feature := range []core.Feature{core.FeatureCheckpoint, core.FeatureFork, core.FeatureSnapshot} {
		if !features.Has(feature) {
			t.Fatalf("%s missing with opt-in: %v", feature, features)
		}
	}
}

func TestCheckpointCapabilityDoesNotChangeClaimScope(t *testing.T) {
	cfg := testConfig()
	cfg.External.Capabilities.IdempotentLeaseID = true
	before, err := externalControllerScope(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.External.Capabilities.Checkpoints = true
	after, err := externalControllerScope(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("scope changed: %s != %s", before, after)
	}
}
