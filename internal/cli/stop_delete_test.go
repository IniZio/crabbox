package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestApplyStopDeleteNoOpWithoutDeleter(t *testing.T) {
	cfg := Config{Provider: "aws"}
	applyStopDelete(&cfg)
	if DeleteOnReleaseExplicit(cfg, "aws") {
		t.Fatal("aws must not be marked explicit")
	}
}

func TestStopDeleteFlagParses(t *testing.T) {
	for _, command := range []string{"stop", "release"} {
		t.Run(command, func(t *testing.T) {
			var stderr bytes.Buffer
			app := App{Stdout: io.Discard, Stderr: &stderr}
			err := app.Run(t.Context(), []string{command, "--delete"})
			if err == nil || !strings.Contains(err.Error(), "usage: crabbox stop") {
				t.Fatalf("err=%v stderr=%q, want stop usage error", err, stderr.String())
			}
		})
	}
}
