package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// maxReverseSocketPathBytes leaves room for the NUL in macOS's 104-byte sun_path.
const maxReverseSocketPathBytes = 103

// reverseTunnelHost is the host end of a reverse forward. Exactly one of port
// (a loopback TCP port) or socket (an absolute Unix socket path) is set.
type reverseTunnelHost struct {
	port   string
	socket string
}

func (h reverseTunnelHost) String() string {
	if h.socket != "" {
		return h.socket
	}
	return net.JoinHostPort(sshTunnelLoopbackHost, h.port)
}

type sshReverseForwardReady struct {
	Box    int    `json:"box"`
	Host   int    `json:"host,omitempty"`
	Socket string `json:"socket,omitempty"`
}

// parseTunnelReverse parses "<boxport>:<hostport>" or "<boxport>:<socket path>".
// The value is not trimmed as a whole, because trailing whitespace belongs to
// a socket path. The port parser trims each port on its own.
func parseTunnelReverse(value string) (string, reverseTunnelHost, error) {
	boxPort, hostEnd, ok := strings.Cut(value, ":")
	if !ok {
		return "", reverseTunnelHost{}, Exit(2, "--reverse must be <boxport>:<hostport> or <boxport>:<socket path>")
	}
	box, err := parseTunnelPort(boxPort, "box port", false)
	if err != nil {
		return "", reverseTunnelHost{}, err
	}
	if port, err := parseTunnelPort(hostEnd, "host port", false); err == nil {
		return box, reverseTunnelHost{port: port}, nil
	}
	if !strings.HasPrefix(hostEnd, "/") {
		return "", reverseTunnelHost{}, Exit(2, "host end must be a TCP port in 1..65535 or an absolute socket path")
	}
	if err := checkReverseSocketPath(hostEnd); err != nil {
		return "", reverseTunnelHost{}, err
	}
	return box, reverseTunnelHost{socket: hostEnd}, nil
}

// checkReverseSocketPath accepts only paths that ssh binds exactly as written.
// ssh -R splits its spec on ':' and expands '%' tokens and '${VAR}' in the
// socket path, so any of those would bind a different path than the one printed.
func checkReverseSocketPath(path string) error {
	if len(path) > maxReverseSocketPathBytes {
		return Exit(2, "host socket path must be at most %d bytes", maxReverseSocketPathBytes)
	}
	if strings.ContainsRune(path, ':') {
		return Exit(2, "host socket path must not contain ':'")
	}
	if strings.ContainsAny(path, "%$") {
		return Exit(2, "host socket path must not contain '%%' or '$'")
	}
	if strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return Exit(2, "host socket path must not contain control characters")
	}
	if strings.TrimSpace(path) != path {
		return Exit(2, "host socket path must not end with whitespace")
	}
	return nil
}

func resolvedSSHReverseTunnelArgs(session *sshTransportSession, boxPort string, host reverseTunnelHost) []string {
	forward := net.JoinHostPort(sshTunnelLoopbackHost, boxPort) + ":" + host.String()
	args := append([]string{}, session.commandPrefix()...)
	return append(args, "-N", "-o", "ExitOnForwardFailure=yes", "-R", forward, session.host())
}

// sshReverseForwardProbe asks the Box whether its loopback port accepts.
func sshReverseForwardProbe(ctx context.Context, target SSHTarget, session *sshTransportSession, boxPort string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	remote := fmt.Sprintf("bash -c 'exec 3<>/dev/tcp/%[1]s/%[2]s' 2>/dev/null || nc -z %[1]s %[2]s", sshTunnelLoopbackHost, boxPort)
	args := append(session.commandPrefix(), "-o", "ConnectTimeout=5", session.host(), remote)
	cmd := exec.CommandContext(probeCtx, directSSHExecutable(), args...)
	applyTargetChildEnvironment(cmd, target)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("box port is not accepting: %w", err)
	}
	return nil
}

// runSSHReverseForward owns one non-multiplexed ssh that exposes the host end
// on the Box's loopback boxPort, until cancelled.
func runSSHReverseForward(ctx context.Context, target SSHTarget, boxPort string, host reverseTunnelHost, asJSON bool, stdout anyWriter, readyTimeout time.Duration) (err error) {
	terminationCtx, stopTerminationSignals := pondMeshTerminationContext(ctx)
	defer stopTerminationSignals()
	ctx = terminationCtx

	session, err := newSSHTransportSession(ctx, target, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, session.Close()) }()

	forwardCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	handle := pondMeshExecCommand(forwardCtx, target, directSSHExecutable(), resolvedSSHReverseTunnelArgs(session, boxPort, host)...)
	output := newSynchronizedTailBuffer(failureTailLines)
	handle.cmd.Stdout = output
	handle.cmd.Stderr = output
	if err := handle.Start(); err != nil {
		return fmt.Errorf("start SSH reverse forward: %w", err)
	}
	type waitResult struct {
		err        error
		terminated bool
	}
	waited := make(chan waitResult, 1)
	go func() {
		waitErr := handle.Wait()
		waited <- waitResult{err: waitErr, terminated: handle.WasTerminatedByOurCancel()}
	}()
	stopAndWait := func() waitResult {
		cancel()
		return <-waited
	}
	diagnostic := func() string { return redactSSHTransportDiagnostic(target, output.String()) }

	deadline := time.NewTimer(readyTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var readinessErr error
	for ready := false; !ready; {
		select {
		case result := <-waited:
			return unexpectedSSHForwardExit(result, diagnostic())
		case <-terminationCtx.Done():
			return cancelledSSHForwardResult(stopAndWait())
		case <-deadline.C:
			result := stopAndWait()
			if cleanupErr := cancelledSSHForwardResult(result); cleanupErr != nil {
				return cleanupErr
			}
			detail := strings.TrimSpace(diagnostic())
			if detail == "" && readinessErr != nil {
				detail = redactSSHTransportDiagnostic(target, readinessErr.Error())
			}
			return Exit(5, "SSH reverse tunnel did not become ready on box %s:%s: %s", sshTunnelLoopbackHost, boxPort, tailForError(detail))
		case <-ticker.C:
			readinessErr = sshReverseForwardProbe(forwardCtx, target, session, boxPort)
			ready = readinessErr == nil
		}
	}
	if asJSON {
		ready := sshReverseForwardReady{Socket: host.socket}
		ready.Box, _ = strconv.Atoi(boxPort)
		if host.socket == "" {
			ready.Host, _ = strconv.Atoi(host.port)
		}
		if encErr := json.NewEncoder(stdout).Encode(ready); encErr != nil {
			stopAndWait()
			return encErr
		}
	} else {
		fmt.Fprintf(stdout, "box %s:%s -> host %s\n", sshTunnelLoopbackHost, boxPort, host)
	}
	select {
	case result := <-waited:
		return unexpectedSSHForwardExit(result, diagnostic())
	case <-terminationCtx.Done():
		return cancelledSSHForwardResult(stopAndWait())
	}
}
