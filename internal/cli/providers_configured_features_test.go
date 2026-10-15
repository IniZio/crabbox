package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

type configuredFeatureTestProvider struct{}

func (configuredFeatureTestProvider) Spec() ProviderSpec {
	return ProviderSpec{Name: "configured-feature-test", Kind: ProviderKindSSHLease, Targets: []TargetSpec{{OS: targetLinux}}}
}
func (configuredFeatureTestProvider) RegisterFlags(*flag.FlagSet, Config) any { return nil }
func (configuredFeatureTestProvider) ApplyFlags(*Config, *flag.FlagSet, any) error {
	return nil
}
func (configuredFeatureTestProvider) Configure(Config, Runtime) (Backend, error) { return nil, nil }
func (configuredFeatureTestProvider) ConfiguredFeatures(cfg Config) []Feature {
	if cfg.External.Capabilities.PauseResume {
		return []Feature{FeaturePauseResume}
	}
	return nil
}

func listConfiguredFeatureTestProvider(t *testing.T, args ...string) []providerMatrixEntry {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := (App{Stdout: &stdout, Stderr: &stderr}).providers(context.Background(), append([]string{"--json"}, args...)); err != nil {
		t.Fatal(err)
	}
	var entries []providerMatrixEntry
	if err := json.Unmarshal(stdout.Bytes(), &entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestProvidersListConfiguredFeatures(t *testing.T) {
	provider := configuredFeatureTestProvider{}
	RegisterProvider(provider)
	t.Cleanup(func() { delete(providerRegistry, provider.Spec().Name) })
	configPath := isolatedConfigPath(t)

	hasFeature := func(entries []providerMatrixEntry) bool {
		for _, entry := range entries {
			if entry.Provider == provider.Spec().Name {
				return FeatureSet(entry.Features).Has(FeaturePauseResume)
			}
		}
		t.Fatalf("provider %q missing from listing", provider.Spec().Name)
		return false
	}

	if hasFeature(listConfiguredFeatureTestProvider(t)) {
		t.Fatal("pause-resume listed without config opt-in")
	}
	if err := os.WriteFile(configPath, []byte("external:\n  capabilities:\n    pauseResume: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !hasFeature(listConfiguredFeatureTestProvider(t)) {
		t.Fatal("pause-resume missing with config opt-in")
	}
	if !hasFeature(listConfiguredFeatureTestProvider(t, "--feature", "pause-resume")) {
		t.Fatal("--feature pause-resume did not match the configured feature")
	}
	if err := os.WriteFile(configPath, []byte("external: [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hasFeature(listConfiguredFeatureTestProvider(t)) {
		t.Fatal("broken config must fall back to the static matrix")
	}
}

func TestExternalPauseResumeCapabilityRequiresTrustedConfig(t *testing.T) {
	var file fileConfig
	if err := yaml.Unmarshal([]byte("external:\n  capabilities:\n    pauseResume: true\n"), &file); err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig()
	if err := applyFileConfigWithTrust(&cfg, file, false); err != nil {
		t.Fatal(err)
	}
	if cfg.External.Capabilities.PauseResume {
		t.Fatal("repository config enabled pauseResume")
	}
	if err := applyFileConfigWithTrust(&cfg, file, true); err != nil {
		t.Fatal(err)
	}
	if !cfg.External.Capabilities.PauseResume {
		t.Fatal("trusted config did not enable pauseResume")
	}

	var disable fileConfig
	if err := yaml.Unmarshal([]byte("external:\n  capabilities:\n    pauseResume: false\n    idempotentLeaseId: true\n"), &disable); err != nil {
		t.Fatal(err)
	}
	if err := applyFileConfigWithTrust(&cfg, disable, false); err != nil {
		t.Fatal(err)
	}
	if !cfg.External.Capabilities.PauseResume || !cfg.External.Capabilities.IdempotentLeaseID {
		t.Fatalf("repository config changed pauseResume or skipped other capabilities: %+v", cfg.External.Capabilities)
	}
}
