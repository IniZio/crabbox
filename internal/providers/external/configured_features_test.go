package external

import (
	"reflect"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestConfiguredFeaturesPauseResume(t *testing.T) {
	cfg := testConfig()
	spec := (Provider{}).Spec().Features
	if got := (Provider{}).ConfiguredFeatures(cfg); !reflect.DeepEqual(got, spec) {
		t.Fatalf("default features = %v, want spec %v", got, spec)
	}

	cfg.External.Capabilities.PauseResume = true
	want := append(append(core.FeatureSet{}, spec...), core.FeaturePauseResume)
	if got := (Provider{}).ConfiguredFeatures(cfg); !reflect.DeepEqual(got, want) {
		t.Fatalf("opt-in features = %v, want %v", got, want)
	}

	cfg.External.Command = ""
	cfg.External.Lifecycle.Acquire.Argv = []string{"devboxctl", "new", "{{name}}"}
	cfg.External.Lifecycle.List.Argv = []string{"devboxctl", "list"}
	cfg.External.Lifecycle.List.Output = lifecycleOutputJSONNameArray
	cfg.External.Lifecycle.Release.Argv = []string{"devboxctl", "rm", "{{name}}"}
	if got := (Provider{}).ConfiguredFeatures(cfg); got.Has(core.FeaturePauseResume) {
		t.Fatalf("lifecycle features = %v, want no pause-resume", got)
	}
}

func TestSpecOmitsPauseResume(t *testing.T) {
	for _, f := range (Provider{}).Spec().Features {
		if f == core.FeaturePauseResume {
			t.Fatal("static spec must not list pause-resume")
		}
	}
}
