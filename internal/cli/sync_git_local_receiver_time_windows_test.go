//go:build windows

package cli

import "testing"

func setLinkMtime(t *testing.T, link string, sec int64) {
	t.Helper()
	t.Skip("POSIX receiver execution")
}
