package cli

import (
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type configuredCacheVolumeTestProvider struct{}

func (configuredCacheVolumeTestProvider) Spec() ProviderSpec {
	return ProviderSpec{Name: "configured-cache-volume-test", Kind: ProviderKindSSHLease, Targets: []TargetSpec{{OS: targetLinux}}}
}
func (configuredCacheVolumeTestProvider) RegisterFlags(*flag.FlagSet, Config) any { return nil }
func (configuredCacheVolumeTestProvider) ApplyFlags(*Config, *flag.FlagSet, any) error {
	return nil
}
func (configuredCacheVolumeTestProvider) Configure(Config, Runtime) (Backend, error) {
	return nil, nil
}
func (configuredCacheVolumeTestProvider) ConfiguredFeatures(cfg Config) FeatureSet {
	if cfg.External.Capabilities.CacheVolumes {
		return FeatureSet{FeatureCacheVolume}
	}
	return nil
}
func (configuredCacheVolumeTestProvider) ValidateRequestedCapabilities(Config) error { return nil }

func registerConfiguredCacheVolumeTestProvider(t *testing.T) string {
	t.Helper()
	provider := configuredCacheVolumeTestProvider{}
	RegisterProvider(provider)
	t.Cleanup(func() { delete(providerRegistry, provider.Spec().Name) })
	return provider.Spec().Name
}

func TestExternalCacheVolumesCapabilityRequiresTrustedConfig(t *testing.T) {
	var file fileConfig
	if err := yaml.Unmarshal([]byte("external:\n  capabilities:\n    cacheVolumes: true\n"), &file); err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig()
	if err := applyFileConfigWithTrust(&cfg, file, false); err != nil {
		t.Fatal(err)
	}
	if cfg.External.Capabilities.CacheVolumes {
		t.Fatal("repository config enabled cacheVolumes")
	}
	if err := applyFileConfigWithTrust(&cfg, file, true); err != nil {
		t.Fatal(err)
	}
	if !cfg.External.Capabilities.CacheVolumes {
		t.Fatal("trusted config did not enable cacheVolumes")
	}

	var disable fileConfig
	if err := yaml.Unmarshal([]byte("external:\n  capabilities:\n    cacheVolumes: false\n    idempotentLeaseId: true\n"), &disable); err != nil {
		t.Fatal(err)
	}
	if err := applyFileConfigWithTrust(&cfg, disable, false); err != nil {
		t.Fatal(err)
	}
	if !cfg.External.Capabilities.CacheVolumes || !cfg.External.Capabilities.IdempotentLeaseID {
		t.Fatalf("repository config changed cacheVolumes or skipped other capabilities: %+v", cfg.External.Capabilities)
	}
}

func TestProvidersListConfiguredCacheVolumeFeature(t *testing.T) {
	name := registerConfiguredCacheVolumeTestProvider(t)
	configPath := isolatedConfigPath(t)
	listed := func(args ...string) bool {
		for _, entry := range listConfiguredFeatureTestProvider(t, args...) {
			if entry.Provider == name {
				return FeatureSet(entry.Features).Has(FeatureCacheVolume)
			}
		}
		t.Fatalf("provider %q missing from listing", name)
		return false
	}
	if listed("--configured") {
		t.Fatal("cache-volume listed without config opt-in")
	}
	if err := os.WriteFile(configPath, []byte("external:\n  capabilities:\n    cacheVolumes: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if listed() {
		t.Fatal("static listing included a configured feature")
	}
	if !listed("--configured") {
		t.Fatal("cache-volume missing with config opt-in")
	}
}

func TestCacheVolumeReadsUseConfiguredFeatures(t *testing.T) {
	name := registerConfiguredCacheVolumeTestProvider(t)
	volume := CacheVolumeConfig{Name: "gradle", Key: "k-gradle", Path: "/opt/gradle", Required: true}
	cfg := baseConfig()
	cfg.Provider = name
	cfg.Cache.Volumes = []CacheVolumeConfig{volume}

	if err := ValidateCacheVolumesForProvider(cfg); err == nil || !strings.Contains(err.Error(), "does not support required cache volume") {
		t.Fatalf("without the capability: err=%v", err)
	}
	if providerSupportsCacheVolumes(cfg, name) {
		t.Fatal("providerSupportsCacheVolumes true without the capability")
	}
	if args := jobLeaseCreateArgsFor(cfg, JobConfig{Provider: name}, true); strings.Contains(strings.Join(args, " "), "--cache-volume") {
		t.Fatalf("job args carry a volume without the capability: %v", args)
	}

	cfg.External.Capabilities.CacheVolumes = true
	if err := ValidateCacheVolumesForProvider(cfg); err != nil {
		t.Fatalf("with the capability: %v", err)
	}
	if !providerSupportsCacheVolumes(cfg, name) {
		t.Fatal("providerSupportsCacheVolumes false with the capability")
	}
	args := jobLeaseCreateArgsFor(cfg, JobConfig{Provider: name}, true)
	if !strings.Contains(strings.Join(args, " "), "--cache-volume k-gradle:/opt/gradle") {
		t.Fatalf("job args miss the volume with the capability: %v", args)
	}
}

func TestParseCacheVolumesEnvJSON(t *testing.T) {
	volumes, err := ParseCacheVolumesEnv(` [
	  {"group":"g1","name":"gradle","key":"g1-gradle","paths":["~/.gradle/caches","~/.gradle/wrapper"],"sizeGB":4,"required":true},
	  {"group":"g1","key":"g1-sdk","path":"/opt/sdk"}
	]`)
	if err != nil {
		t.Fatal(err)
	}
	want := []CacheVolumeConfig{
		{Group: "g1", Name: "gradle", Key: "g1-gradle", Paths: []string{"~/.gradle/caches", "~/.gradle/wrapper"}, SizeGB: 4, Required: true},
		{Group: "g1", Name: "g1-sdk", Key: "g1-sdk", Path: "/opt/sdk"},
	}
	if !reflect.DeepEqual(volumes, want) {
		t.Fatalf("volumes=%#v, want %#v", volumes, want)
	}
	if empty, err := ParseCacheVolumesEnv("[]"); err != nil || len(empty) != 0 {
		t.Fatalf("empty array: %#v, %v", empty, err)
	}
	for _, bad := range []string{`[{"key":"k"}]`, `[{"key":"k","path":"rel"}]`, `[{"key":"k","paths":["x"]}]`, `[not json`} {
		if _, err := ParseCacheVolumesEnv(bad); err == nil {
			t.Fatalf("%s: want error", bad)
		}
	}
}

func TestParseCacheVolumesEnvListFormStillWorks(t *testing.T) {
	volumes, err := ParseCacheVolumesEnv("pnpm=repo-linux-node24-lock:/var/cache/crabbox/pnpm,npm-cache:/var/cache/crabbox/npm")
	if err != nil {
		t.Fatal(err)
	}
	want := []CacheVolumeConfig{
		{Name: "pnpm", Key: "repo-linux-node24-lock", Path: "/var/cache/crabbox/pnpm"},
		{Name: "npm-cache", Key: "npm-cache", Path: "/var/cache/crabbox/npm"},
	}
	if !reflect.DeepEqual(volumes, want) {
		t.Fatalf("volumes=%#v, want %#v", volumes, want)
	}
	if none, err := ParseCacheVolumesEnv(" , "); err != nil || len(none) != 0 {
		t.Fatalf("blank list: %#v, %v", none, err)
	}
}

func TestEnvCacheVolumesJSONReachesConfig(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("CRABBOX_CACHE_VOLUMES", `[{"group":"g1","name":"gradle","key":"g1-gradle","paths":["~/.gradle/caches"]}]`)
	cfg := baseConfig()
	if err := applyEnv(&cfg); err != nil {
		t.Fatal(err)
	}
	want := []CacheVolumeConfig{{Group: "g1", Name: "gradle", Key: "g1-gradle", Paths: []string{"~/.gradle/caches"}}}
	if !reflect.DeepEqual(cfg.Cache.Volumes, want) {
		t.Fatalf("volumes=%#v, want %#v", cfg.Cache.Volumes, want)
	}
}

func TestValidateCacheVolumePerProvider(t *testing.T) {
	long := strings.Repeat("a", 96)
	for _, tc := range []struct {
		name     string
		provider string
		volume   CacheVolumeConfig
		wantErr  string
	}{
		{"external home path", "external", CacheVolumeConfig{Key: "k", Path: "~/.npm"}, ""},
		{"external absolute path", "external", CacheVolumeConfig{Key: "k", Path: "/opt/x"}, ""},
		{"external paths", "external", CacheVolumeConfig{Key: "g-a.b_c", Group: "g", Paths: []string{"~/.a", "/opt/b"}}, ""},
		{"external key at 96", "external", CacheVolumeConfig{Key: long, Path: "~/x"}, ""},
		{"external key at 97", "external", CacheVolumeConfig{Key: long + "a", Path: "~/x"}, "must match"},
		{"external key with space", "external", CacheVolumeConfig{Key: "a b", Path: "~/x"}, "must match"},
		{"external key with slash", "external", CacheVolumeConfig{Key: "a/b", Path: "~/x"}, "must match"},
		{"external key with colon", "external", CacheVolumeConfig{Key: "a:b", Path: "~/x"}, "must not contain ':'"},
		{"external group charset", "external", CacheVolumeConfig{Key: "k", Group: "g/1", Path: "~/x"}, "group"},
		{"external relative path", "external", CacheVolumeConfig{Key: "k", Path: "x/y"}, "must be absolute"},
		{"external bare tilde", "external", CacheVolumeConfig{Key: "k", Path: "~"}, "must be absolute"},
		{"external one bad of many", "external", CacheVolumeConfig{Key: "k", Paths: []string{"~/ok", "nope"}}, "must be absolute"},
		{"external no path", "external", CacheVolumeConfig{Key: "k"}, "path is required"},
		{"other absolute path", "hetzner", CacheVolumeConfig{Key: "k", Path: "/var/cache/x"}, ""},
		{"other home path", "hetzner", CacheVolumeConfig{Key: "k", Path: "~/.npm"}, "must be absolute"},
		{"other paths", "hetzner", CacheVolumeConfig{Key: "k", Path: "/a", Paths: []string{"/a"}}, "only supported by the external provider"},
		{"other group", "hetzner", CacheVolumeConfig{Key: "k", Group: "g", Path: "/a"}, "only supported by the external provider"},
		{"other odd key", "hetzner", CacheVolumeConfig{Key: "a b/c", Path: "/a"}, ""},
		{"other key with colon", "hetzner", CacheVolumeConfig{Key: "a:b", Path: "/a"}, "must not contain ':'"},
		{"parse time home path", "", CacheVolumeConfig{Key: "k", Path: "~/.npm"}, ""},
		{"parse time relative path", "", CacheVolumeConfig{Key: "k", Path: "rel"}, "must be absolute"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCacheVolume(tc.volume, tc.provider)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err=%v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestCacheVolumeStickyDiskSpecsKeepsOnlyAbsoluteSinglePathVolumes(t *testing.T) {
	got := CacheVolumeStickyDiskSpecs([]CacheVolumeConfig{
		{Key: "a", Path: "/opt/a"},
		{Key: "b", Path: "~/.b"},
		{Key: "c", Paths: []string{"/opt/c"}},
	})
	if want := []string{"a:/opt/a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("specs=%v, want %v", got, want)
	}
}

type externalNamedCacheVolumeTestProvider struct {
	configuredCacheVolumeTestProvider
}

func (externalNamedCacheVolumeTestProvider) Spec() ProviderSpec {
	return ProviderSpec{Name: externalCacheVolumeProvider, Kind: ProviderKindSSHLease, Targets: []TargetSpec{{OS: targetLinux}}}
}

func registerExternalNamedCacheVolumeTestProvider(t *testing.T) {
	t.Helper()
	original, had := providerRegistry[externalCacheVolumeProvider]
	providerRegistry[externalCacheVolumeProvider] = externalNamedCacheVolumeTestProvider{}
	t.Cleanup(func() {
		if had {
			providerRegistry[externalCacheVolumeProvider] = original
		} else {
			delete(providerRegistry, externalCacheVolumeProvider)
		}
	})
}

func cacheVolumesFile(t *testing.T, yamlText string) fileConfig {
	t.Helper()
	var file fileConfig
	if err := yaml.Unmarshal([]byte(yamlText), &file); err != nil {
		t.Fatal(err)
	}
	return file
}

const repoCacheVolumesYAML = "cache:\n  volumes:\n    - {group: evil, name: ssh, key: evil-ssh, paths: ['~/.ssh']}\n"
const userCacheVolumesYAML = "cache:\n  volumes:\n    - {group: g1, name: npm, key: g1-npm, paths: ['~/.npm'], sealed: true}\n"

func TestRepoCacheVolumesAreIgnoredForExternalOnly(t *testing.T) {
	cfg := baseConfig()
	if err := applyFileConfigWithTrust(&cfg, cacheVolumesFile(t, repoCacheVolumesYAML), false); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Cache.Volumes) != 1 {
		t.Fatalf("repo volume dropped from cfg.Cache.Volumes: %#v", cfg.Cache.Volumes)
	}
	if got := cfg.CacheVolumesForProvider(externalCacheVolumeProvider); len(got) != 0 {
		t.Fatalf("external sees repo volumes: %#v", got)
	}
	if got := cfg.CacheVolumesForProvider("hetzner"); len(got) != 1 || got[0].Key != "evil-ssh" {
		t.Fatalf("other providers lost the repo volume: %#v", got)
	}
}

func TestTrustedCacheVolumesReachExternal(t *testing.T) {
	cfg := baseConfig()
	if err := applyFileConfigWithTrust(&cfg, cacheVolumesFile(t, userCacheVolumesYAML), true); err != nil {
		t.Fatal(err)
	}
	want := []CacheVolumeConfig{{Group: "g1", Name: "npm", Key: "g1-npm", Paths: []string{"~/.npm"}, Sealed: true}}
	if got := cfg.CacheVolumesForProvider(externalCacheVolumeProvider); !reflect.DeepEqual(got, want) {
		t.Fatalf("trusted volumes = %#v, want %#v", got, want)
	}

	if err := applyFileConfigWithTrust(&cfg, cacheVolumesFile(t, repoCacheVolumesYAML), false); err != nil {
		t.Fatal(err)
	}
	if got := cfg.CacheVolumesForProvider(externalCacheVolumeProvider); !reflect.DeepEqual(got, want) {
		t.Fatalf("a repo volumes list displaced the trusted ones: %#v, want %#v", got, want)
	}
	if len(cfg.Cache.Volumes) != 1 || cfg.Cache.Volumes[0].Key != "evil-ssh" {
		t.Fatalf("other providers must keep replace semantics: %#v", cfg.Cache.Volumes)
	}

	clearConfigEnv(t)
	t.Setenv("CRABBOX_CACHE_VOLUMES", `[{"group":"g2","name":"go","key":"g2-go","paths":["~/go/pkg/mod"],"sealed":true}]`)
	if err := applyEnv(&cfg); err != nil {
		t.Fatal(err)
	}
	envWant := []CacheVolumeConfig{{Group: "g2", Name: "go", Key: "g2-go", Paths: []string{"~/go/pkg/mod"}, Sealed: true}}
	if got := cfg.CacheVolumesForProvider(externalCacheVolumeProvider); !reflect.DeepEqual(got, envWant) {
		t.Fatalf("env volumes = %#v, want %#v", got, envWant)
	}
}

func TestSealedRoundTripsEnvJSONAndYAML(t *testing.T) {
	volumes, err := ParseCacheVolumesEnv(`[{"group":"g1x","name":"docker","key":"g1x-docker","paths":["~/docker","/var/lib/containerd"],"sealed":true},{"group":"g1","name":"npm","key":"g1-npm","paths":["~/.npm"]}]`)
	if err != nil {
		t.Fatal(err)
	}
	if !volumes[0].Sealed || volumes[1].Sealed {
		t.Fatalf("sealed flags = %v, %v", volumes[0].Sealed, volumes[1].Sealed)
	}
	cfg := baseConfig()
	if err := applyFileConfigWithTrust(&cfg, cacheVolumesFile(t, userCacheVolumesYAML), true); err != nil {
		t.Fatal(err)
	}
	if !cfg.Cache.Volumes[0].Sealed {
		t.Fatalf("yaml sealed lost: %#v", cfg.Cache.Volumes)
	}
}

func TestValidateCacheVolumeExternalRoots(t *testing.T) {
	for _, path := range []string{"/var/lib/containerd", "/var/lib/containers", "~/docker"} {
		volume := CacheVolumeConfig{Group: "g1x", Name: "docker", Key: "g1x-docker", Paths: []string{path}, Sealed: true}
		if err := validateCacheVolume(volume, externalCacheVolumeProvider); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	other := CacheVolumeConfig{Key: "k", Path: "/opt/x", Sealed: true}
	if err := validateCacheVolume(other, "hetzner"); err == nil || !strings.Contains(err.Error(), "only supported by the external provider") {
		t.Fatalf("sealed on another provider: err=%v", err)
	}
}

func TestRepoCacheVolumesDoNotLaunderThroughJobArgs(t *testing.T) {
	registerExternalNamedCacheVolumeTestProvider(t)
	hetzner := configuredCacheVolumeTestProvider{}
	RegisterProvider(hetzner)
	t.Cleanup(func() { delete(providerRegistry, hetzner.Spec().Name) })

	repo := cacheVolumesFile(t, "cache:\n  volumes:\n    - {key: repo-vol, path: /opt/repo}\n")
	for provider, wantFlag := range map[string]bool{externalCacheVolumeProvider: false, hetzner.Spec().Name: true} {
		cfg := baseConfig()
		cfg.Provider = provider
		cfg.External.Capabilities.CacheVolumes = true
		if err := applyFileConfigWithTrust(&cfg, repo, false); err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(jobLeaseCreateArgsFor(cfg, JobConfig{Provider: provider}, true), " ")
		if got := strings.Contains(joined, "--cache-volume repo-vol:/opt/repo"); got != wantFlag {
			t.Fatalf("provider=%s: job args %q, want flag=%v", provider, joined, wantFlag)
		}
	}
}
