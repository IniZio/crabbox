package cli

import (
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestParseTunnelPort(t *testing.T) {
	for _, value := range []string{"1", "3000", "65535"} {
		if got, err := parseTunnelPort(value, "remote port", false); err != nil || got != value {
			t.Fatalf("parse %q got=%q err=%v", value, got, err)
		}
	}
	for _, value := range []string{"", "0", "-1", "65536", "http"} {
		if _, err := parseTunnelPort(value, "remote port", false); err == nil {
			t.Fatalf("parse %q unexpectedly succeeded", value)
		}
	}
	if got, err := parseTunnelPort("0", "local port", true); err != nil || got != "" {
		t.Fatalf("auto local port got=%q err=%v", got, err)
	}
}

func TestResolvedSSHTunnelArgsBindLoopback(t *testing.T) {
	session := &sshTransportSession{configPath: "/private/config"}
	args := resolvedSSHTunnelArgs(session, "41000", "3000")
	got := strings.Join(args, " ")
	if !strings.Contains(got, "-L 127.0.0.1:41000:127.0.0.1:3000") {
		t.Fatalf("args=%q", got)
	}
	if strings.Contains(got, "0.0.0.0") || strings.Contains(got, "[::]") {
		t.Fatalf("tunnel exposed non-loopback bind: %q", got)
	}
}

func TestReserveSSHLocalForwardPort(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	reservation, err := reserveSSHLocalForwardPort(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.release()
	if net.ParseIP(sshTunnelLoopbackHost) == nil || reservation.port == "" {
		t.Fatalf("reservation=%#v", reservation)
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort(sshTunnelLoopbackHost, reservation.port))
	if err != nil {
		t.Fatalf("reservation should not occupy TCP before SSH starts: %v", err)
	}
	_ = listener.Close()
}

func TestReserveSSHLocalForwardPortIgnoresUDPUse(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var lastPort string
	var lastErr error
	for attempt := 0; attempt < 32; attempt++ {
		udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			t.Fatal(err)
		}
		port := strconv.Itoa(udp.LocalAddr().(*net.UDPAddr).Port)
		reservation, err := reserveSSHLocalForwardPort(t.Context(), port)
		_ = udp.Close()
		if err == nil {
			reservation.release()
			return
		}
		lastPort, lastErr = port, err
	}
	t.Fatalf("UDP use should not reserve TCP port after 32 attempts; last port %s: %v", lastPort, lastErr)
}

func TestSynchronizedTunnelTailBufferIsBounded(t *testing.T) {
	buffer := newSynchronizedTailBuffer(2)
	for _, line := range []string{"one\n", "two\n", "three\n"} {
		if _, err := buffer.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if got := buffer.String(); got != "two\nthree" {
		t.Fatalf("tail=%q", got)
	}
}

func TestParseTunnelReverse(t *testing.T) {
	box, host, err := parseTunnelReverse("18080:9000")
	if err != nil || box != "18080" || host != "9000" {
		t.Fatalf("got %q %q %v", box, host, err)
	}
	for _, value := range []string{"", "9000", "0:9000", "9000:0", "x:1", "1:x", "65536:1", "1:65536", "1:2:3", ":"} {
		_, _, err := parseTunnelReverse(value)
		var exitErr ExitError
		if err == nil || !AsExitError(err, &exitErr) || exitErr.Code != 2 {
			t.Fatalf("parse %q err=%v, want exit 2", value, err)
		}
	}
}

func TestResolvedSSHReverseTunnelArgsBindLoopback(t *testing.T) {
	session := &sshTransportSession{configPath: "/private/config"}
	got := strings.Join(resolvedSSHReverseTunnelArgs(session, "18080", "9000"), " ")
	if !strings.Contains(got, "-N -o ExitOnForwardFailure=yes -R 127.0.0.1:18080:127.0.0.1:9000") {
		t.Fatalf("args=%q", got)
	}
	if strings.Contains(got, "0.0.0.0") || strings.Contains(got, "[::]") || strings.Contains(got, "-L") {
		t.Fatalf("reverse tunnel exposed non-loopback bind: %q", got)
	}
}

func TestSSHReverseTunnelSessionIsNotMultiplexed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	session, err := newSSHTransportSession(t.Context(), SSHTarget{User: "alice", Host: "example.test", Port: "22", DisableHostKeyChecking: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	config, err := os.ReadFile(session.configPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ControlMaster no", "ControlPath none"} {
		if !strings.Contains(string(config), want) {
			t.Fatalf("session config lacks %q:\n%s", want, config)
		}
	}
}

func TestTunnelReverseUsageErrors(t *testing.T) {
	app := App{Stdout: io.Discard, Stderr: io.Discard}
	for _, args := range [][]string{
		{"--id", "box", "--reverse", "0:9000"},
		{"--id", "box", "--reverse", "8080"},
		{"--id", "box", "--reverse", "8080:9000", "3000"},
		{"--id", "box", "--reverse", "8080:9000", "--local-port", "1"},
		{"--reverse", "8080:9000"},
		{"--id", "box", "--json", "3000"},
	} {
		err := app.tunnel(t.Context(), args)
		var exitErr ExitError
		if err == nil || !AsExitError(err, &exitErr) || exitErr.Code != 2 {
			t.Fatalf("args=%v err=%v, want exit 2", args, err)
		}
	}
}
