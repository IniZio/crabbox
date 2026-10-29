package cli

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWaitForSSHReadyReportsWhyEachAttemptFailed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell ssh fixture")
	}
	const denied = "runner@127.0.0.1: Permission denied (publickey)."
	for _, tc := range []struct {
		name    string
		ssh     string
		want    string
		success bool
	}{
		{
			name: "auth denied",
			ssh:  "printf 'Authorized use only\\n' >&2\nprintf '\\a" + denied + "\\r\\n' >&2\nexit 255\n",
			want: denied,
		},
		{
			name: "ready check fails",
			ssh:  "if [ \"$remote\" = \"exit 0\" ]; then exit 0; fi\nprintf 'private remote output\\n' >&2\nexit 1\n",
			want: "ready check exited 1",
		},
		{name: "ready", ssh: "exit 0\n", success: true},
	} {
		for _, proxy := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/direct", true: "/proxy"}[proxy], func(t *testing.T) {
				dir := t.TempDir()
				script := "#!/bin/sh\nfor remote; do :; done\n" + tc.ssh
				if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				go func() {
					for {
						conn, err := listener.Accept()
						if err != nil {
							return
						}
						conn.Close()
					}
				}()
				host, port, err := net.SplitHostPort(listener.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				target := SSHTarget{
					User: "runner", Host: host, Port: port, FallbackPorts: []string{},
					TargetOS: targetMacOS, SSHConfigProxy: proxy, NoControlMaster: true,
				}
				var progress bytes.Buffer
				err = waitForSSHReady(t.Context(), &target, &progress, "bootstrap", 2*time.Second)
				lines := progress.String()
				if tc.success {
					if err != nil {
						t.Fatalf("wait failed: %v", err)
					}
					if strings.Contains(lines, "last=") {
						t.Fatalf("successful wait reported a failure: %s", lines)
					}
					return
				}
				if err == nil {
					t.Fatal("wait unexpectedly succeeded")
				}
				if got := strings.Count(lines, " last="+tc.want+"\n"); got != 1 {
					t.Fatalf("progress shows cause %d times, want once: %q", got, lines)
				}
				wantErr := "during bootstrap: " + tc.want + ";"
				if !strings.Contains(err.Error(), wantErr) || !strings.Contains(err.Error(), "next_action=") {
					t.Fatalf("timeout does not name the cause %q: %s", wantErr, err)
				}
				for _, private := range []string{"Authorized use only", "private remote output", "\a"} {
					if strings.Contains(lines+err.Error(), private) {
						t.Fatalf("leaked %q: %q / %s", private, lines, err)
					}
				}
				t.Logf("progress: %s", strings.SplitN(lines, "\n", 2)[0])
				t.Logf("timeout: %s", err)
			})
		}
	}
}
