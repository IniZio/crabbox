package external

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

const okReply = `printf '%s' '{"protocolVersion":1}'`

// pauseRequestLine pauses a lease and returns the adapter's raw pause request.
func pauseRequestLine(t *testing.T, capability bool) string {
	t.Helper()
	backend, logPath, _ := pauseResumeBackend(t, true, okReply)
	backend.cfg.External.Capabilities.CacheSaveOnPause = capability
	if err := backend.Pause(context.Background(), core.PauseRequest{ID: "fast-coral"}); err != nil {
		t.Fatal(err)
	}
	return requestLine(t, logPath, "pause")
}

func requestLine(t *testing.T, logPath, op string) string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.Contains(line, `"operation":"`+op+`"`) {
			return line
		}
	}
	t.Fatalf("no %s request in %s", op, data)
	return ""
}

func TestPauseSendsPublishCachesOnlyWhenAsked(t *testing.T) {
	cases := []struct {
		name       string
		set        bool
		value      string
		capability bool
		want       bool
	}{
		{name: "asked with the capability", set: true, value: "1", capability: true, want: true},
		{name: "not asked", capability: true},
		{name: "another value", set: true, value: "0", capability: true},
		{name: "asked without the capability", set: true, value: "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(pausePublishEnv, tc.value)
			}
			line := pauseRequestLine(t, tc.capability)
			if got := strings.Contains(line, `"publishCaches":true`); got != tc.want {
				t.Fatalf("publishCaches sent = %v, want %v: %s", got, tc.want, line)
			}
			if !tc.want && strings.Contains(line, "publishCaches") {
				t.Fatalf("field present: %s", line)
			}
		})
	}
}

func TestOnlyPauseCarriesPublishCaches(t *testing.T) {
	t.Setenv(pausePublishEnv, "1")
	backend, logPath, _ := pauseResumeBackend(t, true, okReply)
	backend.cfg.External.Capabilities.CacheSaveOnPause = true
	if err := backend.Resume(context.Background(), core.ResumeRequest{ID: "fast-coral"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "publishCaches") {
		t.Fatalf("resume or resolve carried the field: %s", data)
	}
}

func TestCacheSaveOnPauseFeatureFollowsTheCapability(t *testing.T) {
	cfg := testConfig()
	cfg.External.Capabilities.PauseResume = true
	if got := (Provider{}).ConfiguredFeatures(cfg); got.Has(core.FeatureCacheSaveOnPause) {
		t.Fatalf("features without the capability = %v", got)
	}
	cfg.External.Capabilities.CacheSaveOnPause = true
	got := (Provider{}).ConfiguredFeatures(cfg)
	if !got.Has(core.FeatureCacheSaveOnPause) {
		t.Fatalf("features with the capability = %v", got)
	}
	if string(core.FeatureCacheSaveOnPause) != "cache-save-on-pause" {
		t.Fatalf("feature token = %s", core.FeatureCacheSaveOnPause)
	}
	for _, f := range (Provider{}).Spec().Features {
		if f == core.FeatureCacheSaveOnPause {
			t.Fatal("static spec must not list cache-save-on-pause")
		}
	}
}

func TestCacheSaveOnPauseCapabilityDoesNotChangeClaimScope(t *testing.T) {
	cfg := testConfig()
	cfg.External.Capabilities.IdempotentLeaseID = true
	before, err := externalControllerScope(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.External.Capabilities.CacheSaveOnPause = true
	after, err := externalControllerScope(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("scope changed: %s != %s", before, after)
	}
}
