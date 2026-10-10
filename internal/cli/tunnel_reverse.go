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
)

type sshReverseForwardReady struct {
	Box  int `json:"box"`
	Host int `json:"host"`
}

// parseTunnelReverse parses "<boxport>:<hostport>"; both must be 1..65535.
func parseTunnelReverse(value string) (string, string, error) {
	boxPort, hostPort, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return "", "", Exit(2, "--reverse must be <boxport>:<hostport>")
	}
	box, err := parseTunnelPort(boxPort, "box port", false)
	if err != nil {
		return "", "", err
	}
	host, err := parseTunnelPort(hostPort, "host port", false)
	if err != nil {
		return "", "", err
	}
	return box, host, nil
}

func resolvedSSHReverseTunnelArgs(session *sshTransportSession, boxPort, hostPort string) []string {
	forward := net.JoinHostPort(sshTunnelLoopbackHost, boxPort) + ":" + net.JoinHostPort(sshTunnelLoopbackHost, hostPort)
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

// runSSHReverseForward owns one non-multiplexed ssh that exposes the host's
// loopback hostPort on the Box's loopback boxPort, until cancelled.
func runSSHReverseForward(ctx context.Context, target SSHTarget, boxPort, hostPort string, asJSON bool, stdout anyWriter, readyTimeout time.Duration) (err error) {
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
	handle := pondMeshExecCommand(forwardCtx, target, directSSHExecutable(), resolvedSSHReverseTunnelArgs(session, boxPort, hostPort)...)
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
		box, _ := strconv.Atoi(boxPort)
		host, _ := strconv.Atoi(hostPort)
		if encErr := json.NewEncoder(stdout).Encode(sshReverseForwardReady{Box: box, Host: host}); encErr != nil {
			stopAndWait()
			return encErr
		}
	} else {
		fmt.Fprintf(stdout, "box %s:%s -> host %s:%s\n", sshTunnelLoopbackHost, boxPort, sshTunnelLoopbackHost, hostPort)
	}
	select {
	case result := <-waited:
		return unexpectedSSHForwardExit(result, diagnostic())
	case <-terminationCtx.Done():
		return cancelledSSHForwardResult(stopAndWait())
	}
}
