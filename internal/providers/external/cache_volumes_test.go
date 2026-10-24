package external

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	core "github.com/openclaw/crabbox/internal/cli"
)

var cacheVolumeFixture = []core.CacheVolumeConfig{
	{Group: "7f3a9c1e04b2d655", Name: "gradle", Key: "7f3a9c1e04b2d655-gradle", Paths: []string{"~/.gradle/caches", "~/.gradle/wrapper"}},
	{Group: "7f3a9c1e04b2d655", Name: "android-sdk", Key: "7f3a9c1e04b2d655-android-sdk", Path: "/opt/android-sdk", SizeGB: 12, Required: true},
}

func cacheVolumeConfig(volumes ...core.CacheVolumeConfig) core.Config {
	cfg := testConfig()
	cfg.External.Capabilities.CacheVolumes = true
	cfg.Cache.Volumes = volumes
	return cfg
}

const cacheVolumeLeaseHead = `{"protocolVersion":1,"lease":{"slug":"shared","name":"devbox-shared","cloudId":"vm1","ssh":{"host":"127.0.0.1","user":"tester","port":"1"}`

func cacheVolumeReply(echo string) string {
	if echo == "" {
		return cacheVolumeLeaseHead + `}}`
	}
	return cacheVolumeLeaseHead + `,"cacheVolumes":` + echo + `}}`
}

// acquireWithCacheVolumes runs one acquire against a scripted adapter. The
// canceled context stops the acquire at SSH readiness, after the point where
// the echo is read and enforced.
func acquireWithCacheVolumes(t *testing.T, cfg core.Config, reply string, extra ...string) (*sequenceRunner, string, error) {
	t.Helper()
	isolateCrabboxState(t)
	runner := &sequenceRunner{responses: append([]string{reply}, extra...)}
	var stderr bytes.Buffer
	backend := &leaseBackend{cfg: cfg, rt: core.Runtime{Stderr: &stderr, Exec: runner}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := backend.Acquire(ctx, core.AcquireRequest{RequestedSlug: "shared", Keep: true})
	return runner, stderr.String(), err
}

func TestConfiguredFeaturesCacheVolume(t *testing.T) {
	cfg := testConfig()
	if got := (Provider{}).ConfiguredFeatures(cfg); got.Has(core.FeatureCacheVolume) {
		t.Fatalf("default features = %v, want no cache-volume", got)
	}
	cfg.External.Capabilities.CacheVolumes = true
	if got := (Provider{}).ConfiguredFeatures(cfg); !got.Has(core.FeatureCacheVolume) || got.Has(core.FeaturePauseResume) {
		t.Fatalf("opt-in features = %v, want cache-volume only", got)
	}
	cfg.External.Capabilities.PauseResume = true
	if got := (Provider{}).ConfiguredFeatures(cfg); !got.Has(core.FeatureCacheVolume) || !got.Has(core.FeaturePauseResume) {
		t.Fatalf("both capabilities = %v", got)
	}

	cfg.External.Command = ""
	cfg.External.Lifecycle.Acquire.Argv = []string{"devboxctl", "new", "{{name}}"}
	cfg.External.Lifecycle.List.Argv = []string{"devboxctl", "list"}
	cfg.External.Lifecycle.List.Output = lifecycleOutputJSONNameArray
	cfg.External.Lifecycle.Release.Argv = []string{"devboxctl", "rm", "{{name}}"}
	if got := (Provider{}).ConfiguredFeatures(cfg); got.Has(core.FeatureCacheVolume) {
		t.Fatalf("lifecycle features = %v, want no cache-volume", got)
	}
}

func TestSpecOmitsCacheVolume(t *testing.T) {
	if (Provider{}).Spec().Features.Has(core.FeatureCacheVolume) {
		t.Fatal("static spec must not list cache-volume")
	}
}

func TestAcquireSendsCacheVolumes(t *testing.T) {
	runner, _, _ := acquireWithCacheVolumes(t, cacheVolumeConfig(cacheVolumeFixture...), cacheVolumeReply(""))
	if len(runner.requests) == 0 || runner.requests[0].Desired == nil {
		t.Fatalf("requests=%#v", runner.requests)
	}
	want := []desiredCacheVolume{
		{Group: "7f3a9c1e04b2d655", Name: "gradle", Key: "7f3a9c1e04b2d655-gradle", Paths: []string{"~/.gradle/caches", "~/.gradle/wrapper"}},
		{Group: "7f3a9c1e04b2d655", Name: "android-sdk", Key: "7f3a9c1e04b2d655-android-sdk", Paths: []string{"/opt/android-sdk"}, SizeGB: 12, Required: true},
	}
	if got := runner.requests[0].Desired.CacheVolumes; !reflect.DeepEqual(got, want) {
		t.Fatalf("desired.cacheVolumes = %#v, want %#v", got, want)
	}
}

func TestDesiredCacheVolumeWireShape(t *testing.T) {
	data, err := json.Marshal(desiredLease{LeaseID: "l", Slug: "s", Name: "n", CacheVolumes: cacheVolumesForProtocol(cacheVolumeFixture)})
	if err != nil {
		t.Fatal(err)
	}
	want := `"cacheVolumes":[` +
		`{"group":"7f3a9c1e04b2d655","name":"gradle","key":"7f3a9c1e04b2d655-gradle","paths":["~/.gradle/caches","~/.gradle/wrapper"]},` +
		`{"group":"7f3a9c1e04b2d655","name":"android-sdk","key":"7f3a9c1e04b2d655-android-sdk","paths":["/opt/android-sdk"],"sizeGB":12,"required":true}]`
	if !strings.Contains(string(data), want) {
		t.Fatalf("wire = %s\nwant containing %s", data, want)
	}
	bare, _ := json.Marshal(desiredLease{LeaseID: "l", Slug: "s", Name: "n"})
	if strings.Contains(string(bare), "cacheVolumes") {
		t.Fatalf("empty volumes leaked onto the wire: %s", bare)
	}
}

func TestAcquireOmitsCacheVolumesWithoutCapability(t *testing.T) {
	cfg := cacheVolumeConfig(cacheVolumeFixture...)
	cfg.External.Capabilities.CacheVolumes = false
	runner, _, _ := acquireWithCacheVolumes(t, cfg, cacheVolumeReply(""))
	if len(runner.requests) == 0 || runner.requests[0].Desired == nil {
		t.Fatalf("requests=%#v", runner.requests)
	}
	if got := runner.requests[0].Desired.CacheVolumes; len(got) != 0 {
		t.Fatalf("volumes sent without the capability: %#v", got)
	}
}

func TestAcquireCacheVolumesFromEnvJSONAndYAML(t *testing.T) {
	loaded := func(t *testing.T, userYAML, envJSON string) core.Config {
		t.Helper()
		home := isolateCrabboxState(t)
		t.Setenv("CRABBOX_CONFIG", "")
		t.Setenv("CRABBOX_PROVIDER", providerName)
		t.Setenv("CRABBOX_CACHE_VOLUMES", envJSON)
		dir := filepath.Join(home, ".config", "crabbox")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(userYAML), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Chdir(t.TempDir())
		cfg, err := core.LoadConfig()
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	capability := "external:\n  capabilities:\n    cacheVolumes: true\n"
	cases := map[string]core.Config{
		"env json": loaded(t, capability, `[{"group":"g1","name":"gradle","key":"g1-gradle","paths":["~/.gradle/caches","~/.gradle/wrapper"],"sizeGB":3},{"group":"g1","name":"sdk","key":"g1-sdk","path":"/opt/sdk"}]`),
		"yaml": loaded(t, capability+"cache:\n  volumes:\n"+
			"    - {group: g1, name: gradle, key: g1-gradle, paths: ['~/.gradle/caches', '~/.gradle/wrapper'], sizeGB: 3}\n"+
			"    - {group: g1, name: sdk, key: g1-sdk, path: /opt/sdk}\n", ""),
	}
	for name, loadedCfg := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := testConfig()
			cfg.External.Capabilities = loadedCfg.External.Capabilities
			cfg.Cache.Volumes = loadedCfg.Cache.Volumes
			runner, _, _ := acquireWithCacheVolumes(t, cfg, cacheVolumeReply(""))
			if len(runner.requests) == 0 || runner.requests[0].Desired == nil {
				t.Fatalf("requests=%#v", runner.requests)
			}
			want := []desiredCacheVolume{
				{Group: "g1", Name: "gradle", Key: "g1-gradle", Paths: []string{"~/.gradle/caches", "~/.gradle/wrapper"}, SizeGB: 3},
				{Group: "g1", Name: "sdk", Key: "g1-sdk", Paths: []string{"/opt/sdk"}},
			}
			if got := runner.requests[0].Desired.CacheVolumes; !reflect.DeepEqual(got, want) {
				t.Fatalf("desired.cacheVolumes = %#v, want %#v", got, want)
			}
		})
	}
}

func TestAcquirePrintsCacheVolumeEchoes(t *testing.T) {
	reply := cacheVolumeReply(`[{"name":"gradle","outcome":"restored","detail":"gen 1729781203"},{"name":"android-sdk","outcome":"empty"}]`)
	_, stderr, _ := acquireWithCacheVolumes(t, cacheVolumeConfig(cacheVolumeFixture[0]), reply)
	for _, line := range []string{
		"cache volume gradle: restored (gen 1729781203)\n",
		"cache volume android-sdk: empty\n",
	} {
		if !strings.Contains(stderr, line) {
			t.Fatalf("stderr missing %q:\n%s", line, stderr)
		}
	}
}

func TestParseCacheVolumeEcho(t *testing.T) {
	var response protocolResponse
	if err := json.Unmarshal([]byte(cacheVolumeReply(`[{"name":"gradle","outcome":"skipped","detail":"store full"}]`)), &response); err != nil {
		t.Fatal(err)
	}
	want := []cacheVolumeEcho{{Name: "gradle", Outcome: "skipped", Detail: "store full"}}
	if !reflect.DeepEqual(response.Lease.CacheVolumes, want) {
		t.Fatalf("echo = %#v, want %#v", response.Lease.CacheVolumes, want)
	}
}

func TestAcquireRollsBackWhenRequiredCacheVolumeSkipped(t *testing.T) {
	required := core.CacheVolumeConfig{Name: "gradle", Key: "k-gradle", Path: "/opt/gradle", Required: true}
	optional := core.CacheVolumeConfig{Name: "gradle", Key: "k-gradle", Path: "/opt/gradle"}
	skipped := cacheVolumeReply(`[{"name":"gradle","outcome":"skipped","detail":"store full"}]`)

	runner, stderr, err := acquireWithCacheVolumes(t, cacheVolumeConfig(required), skipped, `{"protocolVersion":1}`)
	if err == nil || !strings.Contains(err.Error(), `required cache volume "gradle"`) {
		t.Fatalf("err=%v", err)
	}
	if !reflect.DeepEqual(runner.operations, []string{"acquire", "release"}) {
		t.Fatalf("operations=%v, want acquire then release", runner.operations)
	}
	if !strings.Contains(stderr, "cache volume gradle: skipped (store full)") {
		t.Fatalf("stderr=%q", stderr)
	}

	runner, _, err = acquireWithCacheVolumes(t, cacheVolumeConfig(optional), skipped)
	if err != nil && strings.Contains(err.Error(), "cache volume") {
		t.Fatalf("optional skip failed the acquire: %v", err)
	}
	if !reflect.DeepEqual(runner.operations, []string{"acquire"}) {
		t.Fatalf("optional skip rolled back: operations=%v", runner.operations)
	}
}

func TestClaimScopeIgnoresCacheVolumesCapability(t *testing.T) {
	for _, idempotent := range []bool{false, true} {
		off := testConfig()
		off.External.Capabilities.IdempotentLeaseID = idempotent
		on := off
		on.External.Capabilities.CacheVolumes = true
		if externalClaimScope(off) != externalClaimScope(on) {
			t.Fatalf("idempotentLeaseId=%t: cacheVolumes changed the claim scope", idempotent)
		}
	}
	base := testConfig()
	base.External.Capabilities.IdempotentLeaseID = true
	other := base
	other.External.Config = map[string]any{"namespace": "other"}
	if externalClaimScope(base) == externalClaimScope(other) {
		t.Fatal("control: scope must still depend on adapter config")
	}
}

func TestValidateCacheVolumesForExternalProvider(t *testing.T) {
	cfg := cacheVolumeConfig(cacheVolumeFixture...)
	cfg.Provider = providerName
	if err := core.ValidateCacheVolumesForProvider(cfg); err != nil {
		t.Fatalf("home paths and required volumes with the capability: %v", err)
	}
	cfg.Cache.Volumes = []core.CacheVolumeConfig{{Name: "x", Key: "has space", Path: "~/x"}}
	if err := core.ValidateCacheVolumesForProvider(cfg); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("bad key: err=%v", err)
	}
	cfg = cacheVolumeConfig(cacheVolumeFixture...)
	cfg.Provider = providerName
	cfg.External.Capabilities.CacheVolumes = false
	if err := core.ValidateCacheVolumesForProvider(cfg); err == nil || !strings.Contains(err.Error(), "does not support required cache volume") {
		t.Fatalf("required volume without the capability: err=%v", err)
	}
}

// loadedCacheVolumeConfig runs the real config loader: a trusted user config,
// a repo crabbox.yaml in the working directory, and the env var.
func loadedCacheVolumeConfig(t *testing.T, userYAML, repoYAML, envJSON string) core.Config {
	t.Helper()
	home := isolateCrabboxState(t)
	t.Setenv("CRABBOX_CONFIG", "")
	t.Setenv("CRABBOX_PROVIDER", providerName)
	t.Setenv("CRABBOX_CACHE_VOLUMES", envJSON)
	dir := filepath.Join(home, ".config", "crabbox")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(userYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "crabbox.yaml"), []byte(repoYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	cfg, err := core.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func sentCacheVolumes(t *testing.T, loaded core.Config) []desiredCacheVolume {
	t.Helper()
	cfg := testConfig()
	cfg.External.Capabilities = loaded.External.Capabilities
	cfg.Cache = loaded.Cache
	runner, _, _ := acquireWithCacheVolumes(t, cfg, cacheVolumeReply(""))
	if len(runner.requests) == 0 || runner.requests[0].Desired == nil {
		t.Fatalf("requests=%#v", runner.requests)
	}
	return runner.requests[0].Desired.CacheVolumes
}

const (
	cacheVolumeCapabilityYAML = "external:\n  capabilities:\n    cacheVolumes: true\n"
	repoEvilVolumeYAML        = "cache:\n  volumes:\n    - {group: evil, name: ssh, key: evil-ssh, paths: ['~/.ssh']}\n"
)

func TestAcquireIgnoresRepoCacheVolumes(t *testing.T) {
	if got := sentCacheVolumes(t, loadedCacheVolumeConfig(t, cacheVolumeCapabilityYAML, repoEvilVolumeYAML, "")); len(got) != 0 {
		t.Fatalf("repo volumes reached the adapter: %#v", got)
	}

	userYAML := cacheVolumeCapabilityYAML + "cache:\n  volumes:\n    - {group: g1, name: npm, key: g1-npm, paths: ['~/.npm']}\n"
	want := []desiredCacheVolume{{Group: "g1", Name: "npm", Key: "g1-npm", Paths: []string{"~/.npm"}}}
	if got := sentCacheVolumes(t, loadedCacheVolumeConfig(t, userYAML, repoEvilVolumeYAML, "")); !reflect.DeepEqual(got, want) {
		t.Fatalf("trusted config volumes with a repo list: %#v, want %#v", got, want)
	}

	envJSON := `[{"group":"g2","name":"go","key":"g2-go","paths":["~/go/pkg/mod"]}]`
	envWant := []desiredCacheVolume{{Group: "g2", Name: "go", Key: "g2-go", Paths: []string{"~/go/pkg/mod"}}}
	if got := sentCacheVolumes(t, loadedCacheVolumeConfig(t, cacheVolumeCapabilityYAML, repoEvilVolumeYAML, envJSON)); !reflect.DeepEqual(got, envWant) {
		t.Fatalf("env volumes with a repo list: %#v, want %#v", got, envWant)
	}
}

func TestAcquireForwardsSealedFromEnvJSONAndTrustedYAML(t *testing.T) {
	want := []desiredCacheVolume{
		{Group: "g1x", Name: "docker", Key: "g1x-docker", Paths: []string{"~/docker", "/var/lib/containerd"}, Sealed: true},
		{Group: "g1", Name: "npm", Key: "g1-npm", Paths: []string{"~/.npm"}},
	}
	envJSON := `[{"group":"g1x","name":"docker","key":"g1x-docker","paths":["~/docker","/var/lib/containerd"],"sealed":true},{"group":"g1","name":"npm","key":"g1-npm","paths":["~/.npm"]}]`
	if got := sentCacheVolumes(t, loadedCacheVolumeConfig(t, cacheVolumeCapabilityYAML, "", envJSON)); !reflect.DeepEqual(got, want) {
		t.Fatalf("env: %#v, want %#v", got, want)
	}
	userYAML := cacheVolumeCapabilityYAML + "cache:\n  volumes:\n" +
		"    - {group: g1x, name: docker, key: g1x-docker, paths: ['~/docker', /var/lib/containerd], sealed: true}\n" +
		"    - {group: g1, name: npm, key: g1-npm, paths: ['~/.npm']}\n"
	if got := sentCacheVolumes(t, loadedCacheVolumeConfig(t, userYAML, "", "")); !reflect.DeepEqual(got, want) {
		t.Fatalf("trusted yaml: %#v, want %#v", got, want)
	}
	sealedWire, _ := json.Marshal(want[0])
	plainWire, _ := json.Marshal(want[1])
	if !strings.Contains(string(sealedWire), `"sealed":true`) || strings.Contains(string(plainWire), "sealed") {
		t.Fatalf("wire: %s / %s", sealedWire, plainWire)
	}
}

func TestValidateCacheVolumesForExternalAcceptsContainerRoots(t *testing.T) {
	cfg := cacheVolumeConfig(core.CacheVolumeConfig{Group: "g1x", Name: "docker", Key: "g1x-docker", Paths: []string{"~/docker", "/var/lib/containerd", "/var/lib/containers"}, Sealed: true})
	cfg.Provider = providerName
	if err := core.ValidateCacheVolumesForProvider(cfg); err != nil {
		t.Fatal(err)
	}
}

const cacheScopeFixture = "0123456789abcdef"

func TestAcquireSendsCacheScopeWithVolumes(t *testing.T) {
	cfg := cacheVolumeConfig(cacheVolumeFixture...)
	cfg.Cache.Scope = cacheScopeFixture
	runner, _, _ := acquireWithCacheVolumes(t, cfg, cacheVolumeReply(""))
	if len(runner.requests) == 0 || runner.requests[0].Desired == nil {
		t.Fatalf("requests=%#v", runner.requests)
	}
	if got := runner.requests[0].Desired.CacheScope; got != cacheScopeFixture {
		t.Fatalf("desired.cacheScope = %q, want %q", got, cacheScopeFixture)
	}
	data, err := json.Marshal(desiredLease{LeaseID: "l", Slug: "s", Name: "n", CacheScope: cacheScopeFixture})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"cacheScope":"`+cacheScopeFixture+`"`) {
		t.Fatalf("wire form = %s", data)
	}
	bare, _ := json.Marshal(desiredLease{LeaseID: "l", Slug: "s", Name: "n"})
	if strings.Contains(string(bare), "cacheScope") {
		t.Fatalf("empty scope serialized: %s", bare)
	}
}

func TestAcquireOmitsCacheScopeWithoutVolumes(t *testing.T) {
	cfg := cacheVolumeConfig()
	cfg.Cache.Scope = cacheScopeFixture
	runner, _, _ := acquireWithCacheVolumes(t, cfg, cacheVolumeReply(""))
	if len(runner.requests) == 0 || runner.requests[0].Desired == nil {
		t.Fatalf("requests=%#v", runner.requests)
	}
	if got := runner.requests[0].Desired.CacheScope; got != "" {
		t.Fatalf("scope sent without volumes: %q", got)
	}
	cfg = cacheVolumeConfig(cacheVolumeFixture...)
	cfg.Cache.Scope = cacheScopeFixture
	cfg.External.Capabilities.CacheVolumes = false
	runner, _, _ = acquireWithCacheVolumes(t, cfg, cacheVolumeReply(""))
	if got := runner.requests[0].Desired.CacheScope; got != "" {
		t.Fatalf("scope sent without the capability: %q", got)
	}
}

func TestClaimScopeIgnoresCacheScope(t *testing.T) {
	for _, idempotent := range []bool{false, true} {
		off := cacheVolumeConfig(cacheVolumeFixture...)
		off.External.Capabilities.IdempotentLeaseID = idempotent
		on := off
		on.Cache.Scope = cacheScopeFixture
		if externalClaimScope(off) != externalClaimScope(on) {
			t.Fatalf("idempotentLeaseId=%t: cache scope changed the claim scope", idempotent)
		}
	}
}
