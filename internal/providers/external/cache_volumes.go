package external

import (
	"fmt"
	"io"
	"strings"

	core "github.com/openclaw/crabbox/internal/cli"
)

// cacheVolumesEnabled is the single gate for sending volumes: the trusted
// capability, and no declarative lifecycle (which has no volume operation).
func (b *leaseBackend) cacheVolumesEnabled() bool {
	return b.cfg.External.Capabilities.CacheVolumes && !lifecycleConfigured(b.cfg.External)
}

func cacheVolumesForProtocol(volumes []core.CacheVolumeConfig) []desiredCacheVolume {
	desired := make([]desiredCacheVolume, 0, len(volumes))
	for _, volume := range volumes {
		desired = append(desired, desiredCacheVolume{
			Group:    volume.Group,
			Name:     volume.Name,
			Key:      volume.Key,
			Paths:    append([]string(nil), volume.EffectivePaths()...),
			SizeGB:   volume.SizeGB,
			Required: volume.Required,
		})
	}
	return desired
}

func printCacheVolumeEchoes(w io.Writer, echoes []cacheVolumeEcho) {
	if w == nil {
		return
	}
	for _, echo := range echoes {
		line := fmt.Sprintf("cache volume %s: %s", echo.Name, echo.Outcome)
		if echo.Detail != "" {
			line += " (" + echo.Detail + ")"
		}
		fmt.Fprintln(w, line)
	}
}

// enforceRequiredCacheVolumes fails when the adapter skipped a volume the
// caller marked required. A volume that is not required may be skipped.
func enforceRequiredCacheVolumes(asked []desiredCacheVolume, echoed []cacheVolumeEcho) error {
	for _, echo := range echoed {
		if !strings.EqualFold(strings.TrimSpace(echo.Outcome), cacheVolumeOutcomeSkipped) {
			continue
		}
		for _, volume := range asked {
			if volume.Required && volume.Name == echo.Name {
				return core.Exit(5, "external provider skipped required cache volume %q: %s", volume.Name, firstNonEmpty(echo.Detail, "no detail"))
			}
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
