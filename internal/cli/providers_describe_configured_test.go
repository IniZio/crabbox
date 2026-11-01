package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

type describeConfiguredTestProvider struct{ configuredFeatureTestProvider }

func (describeConfiguredTestProvider) Spec() ProviderSpec {
	spec := configuredFeatureTestProvider{}.Spec()
	spec.Name = "describe-configured-test"
	return spec
}

func (describeConfiguredTestProvider) ConfiguredFeatures(cfg Config) FeatureSet {
	if len(cfg.External.Lifecycle.Acquire.Argv) > 0 {
		return nil
	}
	if cfg.External.Capabilities.CacheVolumes {
		return FeatureSet{FeatureCacheVolume}
	}
	return nil
}

func describeConfiguredFeatures(t *testing.T, args ...string) []string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args = append([]string{"--json"}, append(args, "describe-configured-test")...)
	if err := (App{Stdout: &stdout, Stderr: &stderr}).providerDescribe(args); err != nil {
		t.Fatal(err)
	}
	var description providerDescription
	if err := json.Unmarshal(stdout.Bytes(), &description); err != nil {
		t.Fatal(err)
	}
	return description.Capabilities.Features
}

func hasDescribedFeature(features []string, want Feature) bool {
	for _, feature := range features {
		if feature == string(want) {
			return true
		}
	}
	return false
}

func TestProvidersDescribeConfiguredFeatures(t *testing.T) {
	provider := describeConfiguredTestProvider{}
	RegisterProvider(provider)
	t.Cleanup(func() { delete(providerRegistry, provider.Spec().Name) })
	configPath := isolatedConfigPath(t)
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("external:\n  capabilities:\n    cacheVolumes: true\n")
	if hasDescribedFeature(describeConfiguredFeatures(t), FeatureCacheVolume) {
		t.Fatal("static describe included a configured feature")
	}
	if !hasDescribedFeature(describeConfiguredFeatures(t, "--configured"), FeatureCacheVolume) {
		t.Fatal("--configured did not add cache-volume with cacheVolumes enabled")
	}

	write("external:\n  capabilities:\n    cacheVolumes: false\n")
	if hasDescribedFeature(describeConfiguredFeatures(t, "--configured"), FeatureCacheVolume) {
		t.Fatal("--configured added cache-volume without the opt-in")
	}

	write("external:\n  capabilities:\n    cacheVolumes: true\n  lifecycle:\n    acquire:\n      argv: [true]\n")
	if hasDescribedFeature(describeConfiguredFeatures(t, "--configured"), FeatureCacheVolume) {
		t.Fatal("--configured added cache-volume with declarative lifecycle configured")
	}
}
