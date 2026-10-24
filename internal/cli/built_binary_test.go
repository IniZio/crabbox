package cli

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openclaw/crabbox/internal/testutil"
)

var cliTestBuild struct {
	once   sync.Once
	dir    string
	binary string
	err    error
}

// Own the immutable CLI across roots and -count iterations, without building
// anything unless a binary contract requests it during m.Run.
func builtCLITestBinary() (string, error) {
	cliTestBuild.once.Do(func() {
		dir, err := os.MkdirTemp("", "crabbox-cli-test-build-")
		if err != nil {
			cliTestBuild.err = fmt.Errorf("create CLI test build directory: %w", err)
			return
		}
		cliTestBuild.dir = dir
		binary := filepath.Join(dir, "crabbox")
		build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/crabbox")
		build.Dir = filepath.Join("..", "..")
		if output, err := build.CombinedOutput(); err != nil {
			cliTestBuild.err = fmt.Errorf("go build ./cmd/crabbox: %w\n%s", err, output)
			return
		}
		cliTestBuild.binary = binary
	})
	return cliTestBuild.binary, cliTestBuild.err
}

func runCLITests(m *testing.M) (code int) {
	if len(os.Args) == 3 && os.Args[1] == testTCPRelayArg {
		return relayTestTCP(os.Args[2])
	}
	if os.Getenv("CRABBOX_TEST_SERVE_RELAY_PROXY") == "1" && len(os.Args) > 1 && os.Args[1] == "__serve-relay-proxy" {
		if err := Run(context.Background(), os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
	actionsHydrationStopSettleDelay = time.Millisecond
	localActionsHydrationPollInterval = 50 * time.Millisecond
	defer func() {
		if cliTestBuild.dir != "" {
			// Capture roots rebuild the adjacent .provider fixture after their
			// owned children finish; retain t.TempDir's cleanup failure reporting.
			if err := os.RemoveAll(cliTestBuild.dir); err != nil {
				fmt.Fprintf(os.Stderr, "remove CLI test build directory: %v\n", err)
				if code == 0 {
					code = 1
				}
			}
		}
	}()
	return testutil.RunWithIsolatedUserDirs(m)
}

// runCLITests shortens the hydration stop grace for the whole package; a test
// that asserts the grace itself must run against the product value.
func useProductActionsHydrationStopGrace(t *testing.T) {
	t.Helper()
	previous := actionsHydrationStopSettleDelay
	actionsHydrationStopSettleDelay = actionsHydrationStopGrace
	t.Cleanup(func() { actionsHydrationStopSettleDelay = previous })
}

const testTCPRelayArg = "__crabbox-test-tcp-relay"

// relayTestTCP lets the test binary serve as an SSH ProxyCommand.
func relayTestTCP(address string) int {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	go func() {
		_, _ = io.Copy(conn, os.Stdin)
		_ = conn.(*net.TCPConn).CloseWrite()
	}()
	_, _ = io.Copy(os.Stdout, conn)
	return 0
}
