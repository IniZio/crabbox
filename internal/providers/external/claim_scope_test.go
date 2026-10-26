package external

import (
	"testing"
	"time"

	core "github.com/openclaw/crabbox/internal/cli"
)

func TestHeartbeatScopeMatchesTheScopeAcquireClaims(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := core.Config{Provider: providerName}
	cfg.External.Command = "/opt/provider/plugin"
	cfg.External.Config = map[string]any{"owner": "moor", "region": "us"}
	cfg.External.Capabilities.IdempotentLeaseID = true
	backend := &leaseBackend{cfg: cfg}
	if err := backend.claimLeaseForRepo("cbx_0123456789ab", "box-1", t.TempDir(), time.Minute, false); err != nil {
		t.Fatalf("claim lease: %v", err)
	}
	claim, ok, err := core.ResolveLeaseClaimForProvider("box-1", providerName)
	if err != nil || !ok {
		t.Fatalf("resolve claim ok=%v err=%v", ok, err)
	}
	if got := core.ProviderClaimScope(providerName, cfg); got != claim.ProviderScope {
		t.Fatalf("heartbeat and status compare scope %q, acquire claimed %q", got, claim.ProviderScope)
	}
}
