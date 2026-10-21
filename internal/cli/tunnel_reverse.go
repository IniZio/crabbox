package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
	// -v makes ssh log the server's reply to the forward request, which is the
	// readiness signal. It overrides the config's LogLevel.
	return append(args, "-v", "-N", "-o", "ExitOnForwardFailure=yes", "-R", forward, session.host())
}

type reverseForwardState int

const (
	reverseForwardPending reverseForwardState = iota
	reverseForwardReady
	reverseForwardFailed
)

// classifyReverseForwardLine reads one line of ssh -v stderr. OpenSSH logs
// "remote forward success for: listen <host>:<port>, connect <target>" when
// the server accepts the -R request, and "remote forward failure ..." when it
// refuses it.
func classifyReverseForwardLine(line, listen string) reverseForwardState {
	line = strings.TrimPrefix(line, "debug1: ")
	switch {
	case strings.HasPrefix(line, "remote forward success for: listen "+listen+","):
		return reverseForwardReady
	case strings.HasPrefix(line, "remote forward failure for: listen "):
		return reverseForwardFailed
	}
	return reverseForwardPending
}

// maxReverseForwardLineBytes bounds a stderr line that never ends.
const maxReverseForwardLineBytes = 64 << 10

// reverseForwardWatcher is the forward ssh's stderr. It reports the first
// success or failure line once on outcome, and keeps every line except ssh's
// debug lines in tail for diagnostics.
type reverseForwardWatcher struct {
	listen  string
	tail    io.Writer
	outcome chan reverseForwardState
	partial []byte
	decided bool
}

func newReverseForwardWatcher(listen string, tail io.Writer) *reverseForwardWatcher {
	return &reverseForwardWatcher{listen: listen, tail: tail, outcome: make(chan reverseForwardState, 1)}
}

func (w *reverseForwardWatcher) Write(p []byte) (int, error) {
	w.partial = append(w.partial, p...)
	for {
		end := bytes.IndexByte(w.partial, '\n')
		if end < 0 {
			break
		}
		w.line(string(w.partial[:end]))
		w.partial = w.partial[end+1:]
	}
	if len(w.partial) > maxReverseForwardLineBytes {
		w.line(string(w.partial))
		w.partial = nil
	}
	return len(p), nil
}

func (w *reverseForwardWatcher) line(line string) {
	line = strings.TrimRight(line, "\r")
	state := classifyReverseForwardLine(line, w.listen)
	if state == reverseForwardFailed || !strings.HasPrefix(line, "debug") {
		_, _ = io.WriteString(w.tail, strings.TrimPrefix(line, "debug1: ")+"\n")
	}
	if state != reverseForwardPending && !w.decided {
		w.decided = true
		w.outcome <- state
	}
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
	watcher := newReverseForwardWatcher(net.JoinHostPort(sshTunnelLoopbackHost, boxPort), output)
	handle.cmd.Stdout = output
	handle.cmd.Stderr = watcher
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
		return Exit(5, "SSH reverse tunnel did not become ready on box %s:%s: %s", sshTunnelLoopbackHost, boxPort, tailForError(strings.TrimSpace(diagnostic())))
	case state := <-watcher.outcome:
		if state == reverseForwardFailed {
			stopAndWait()
			return Exit(5, "SSH reverse tunnel was refused on box %s:%s: %s", sshTunnelLoopbackHost, boxPort, tailForError(strings.TrimSpace(diagnostic())))
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
