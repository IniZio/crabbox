package sprites

import (
	"strings"

	core "github.com/openclaw/crabbox/internal/cli"
)

func (Provider) CommandRouting(cfg core.Config, _ core.CommandRoutingRequest) core.CommandRouting {
	var args []string
	if strings.TrimSpace(cfg.Sprites.APIURL) != "" {
		args = append(args, "--sprites-api-url", core.RoutingSafeURL(cfg.Sprites.APIURL))
	}
	if strings.TrimSpace(cfg.Sprites.WorkRoot) != "" {
		args = append(args, "--sprites-work-root", cfg.Sprites.WorkRoot)
	}
	if len(cfg.Sprites.NetworkAllow) > 0 {
		args = append(args, "--sprites-network-allow", strings.Join(cfg.Sprites.NetworkAllow, ","))
	}
	return core.CommandRouting{Args: args}
}
