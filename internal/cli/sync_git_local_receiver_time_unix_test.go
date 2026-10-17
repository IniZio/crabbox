//go:build !windows

package cli

import (
	"testing"

	"golang.org/x/sys/unix"
)

func setLinkMtime(t *testing.T, link string, sec int64) {
	t.Helper()
	if err := unix.Lutimes(link, []unix.Timeval{{Sec: sec}, {Sec: sec}}); err != nil {
		t.Fatal(err)
	}
}
