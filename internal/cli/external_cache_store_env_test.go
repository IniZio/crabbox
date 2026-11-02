package cli

import (
	"strings"
	"testing"
)

func TestCacheStoreMaxGBEnvOverridesExternalConfig(t *testing.T) {
	t.Setenv("CRABBOX_CACHE_STORE_MAX_GB", "50")
	cfg := baseConfig()
	fileConfig := map[string]any{"cacheStoreMaxGB": 200, "other": "kept"}
	cfg.External.Config = fileConfig
	if err := applyEnv(&cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.External.Config["cacheStoreMaxGB"]; got != 50 {
		t.Fatalf("cacheStoreMaxGB=%#v, want 50", got)
	}
	if cfg.External.Config["other"] != "kept" {
		t.Fatalf("unrelated keys lost: %#v", cfg.External.Config)
	}
	if fileConfig["cacheStoreMaxGB"] != 200 {
		t.Fatalf("file-derived map mutated: %#v", fileConfig)
	}
}

func TestCacheStoreMaxGBEnvSetsKeyWithoutFileConfig(t *testing.T) {
	t.Setenv("CRABBOX_CACHE_STORE_MAX_GB", "50")
	cfg := baseConfig()
	if err := applyEnv(&cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.External.Config["cacheStoreMaxGB"]; got != 50 {
		t.Fatalf("cacheStoreMaxGB=%#v, want 50", got)
	}
}

func TestCacheStoreMaxGBEnvUnsetLeavesConfig(t *testing.T) {
	t.Setenv("CRABBOX_CACHE_STORE_MAX_GB", "")
	cfg := baseConfig()
	cfg.External.Config = map[string]any{"cacheStoreMaxGB": 200}
	if err := applyEnv(&cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.External.Config["cacheStoreMaxGB"]; got != 200 {
		t.Fatalf("cacheStoreMaxGB=%#v, want 200", got)
	}
	bare := baseConfig()
	if err := applyEnv(&bare); err != nil {
		t.Fatal(err)
	}
	if _, ok := bare.External.Config["cacheStoreMaxGB"]; ok {
		t.Fatalf("key set without env: %#v", bare.External.Config)
	}
}

func TestCacheStoreMaxGBEnvRejectsInvalid(t *testing.T) {
	for _, value := range []string{"abc", "0", "-3", "1.5"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CRABBOX_CACHE_STORE_MAX_GB", value)
			cfg := baseConfig()
			err := applyEnv(&cfg)
			if err == nil || !strings.Contains(err.Error(), "CRABBOX_CACHE_STORE_MAX_GB") {
				t.Fatalf("err=%v, want mention of CRABBOX_CACHE_STORE_MAX_GB", err)
			}
		})
	}
}
