# tunnel

`crabbox tunnel` opens one foreground SSH local port-forward to a resolved
lease. The local listener and remote destination are both fixed to
`127.0.0.1`; only the port numbers vary.

```sh
crabbox tunnel --id blue-box 3000
crabbox tunnel --id blue-box --local-port 8080 3000
crabbox tunnel --provider ssh --id buildbox 4173
```

The positional argument is the remote TCP port. `--local-port` is optional. If
it is omitted or set to `0`, Crabbox chooses an available local port.

## Readiness and output

The command prints exactly one URL after the tracked SSH process owns an IPv4
loopback listener and a TCP connection to that listener succeeds:

```text
http://127.0.0.1:49152
```

It does not print the URL merely because `ssh` launched. The URL is convenient
for HTTP development servers; the forward itself is ordinary TCP.

After printing readiness, the command stays in the foreground until Ctrl-C,
SIGTERM, or parent context cancellation. Teardown uses the same owned
process-group/job-object path as pond SSH forwards, so the SSH root and any
ProxyCommand descendants are reaped together.

## Reverse tunnel

`--reverse <boxport>:<hostport>` does the opposite: it exposes a port on this
machine to the lease. The Box listens on `127.0.0.1:<boxport>` and each
connection is forwarded to `127.0.0.1:<hostport>` here. Both ends are
loopback only (`ssh -N -o ExitOnForwardFailure=yes -R
127.0.0.1:<boxport>:127.0.0.1:<hostport>`); there is no positional port and no
`--local-port`.

`--reverse <boxport>:<socket path>` forwards each connection to a Unix socket
on this machine instead (`ssh -R 127.0.0.1:<boxport>:<socket path>`). The path
must be absolute and at most 103 bytes, the macOS limit. It must not contain
`:`, `%`, `$`, control characters (newline, tab), or leading or trailing
whitespace. `ssh -R` splits its spec on `:` and expands `%` tokens and `${VAR}`
in the path, so those characters would bind a different path than the one
printed. Spaces inside the path are allowed. The socket can be
missing when the tunnel starts. The ssh client connects to the socket only
when a Box connection arrives, so the running tunnel reaches a server that
binds the same path again. On this machine, only users who can open the socket
file can connect to it. Put it in a directory that only you can enter.

```sh
crabbox tunnel --id blue-box --reverse 18080:9000 --json
crabbox tunnel --id blue-box --reverse 18080:/tmp/app-1000/front.sock --json
```

The reverse forward uses its own non-multiplexed SSH session, started with
`-v`. Readiness is OpenSSH's own `remote forward success` reply for the Box
port, so no extra connection is opened; until then nothing is printed. A lease
whose provider pins its host key behind a proxy command instead adds the
forward to the connection that checked the lease's readiness (`ssh -O
forward`), so the tunnel opens one SSH connection in all; the master replies
only once the server accepts the forward, and the tunnel exits with status 5
if that connection closes. After that it prints one line and blocks until
Ctrl-C, SIGTERM, or context cancellation, which reaps the SSH process group. With
`--json` the line is `{"box":18080,"host":9000}`, or
`{"box":18080,"socket":"/tmp/app-1000/front.sock"}` for a socket; otherwise
`box 127.0.0.1:18080 -> host 127.0.0.1:9000` or
`box 127.0.0.1:18080 -> host /tmp/app-1000/front.sock`. Both ports must be in
1..65535. A socket path that is relative, over 103 bytes, or contains a
character listed above is also a usage error (exit 2). If the Box port is not
ready within 15 seconds, or the server sends `remote forward failure`, or SSH
exits first, the command exits 5 with a redacted diagnostic.

## Flags

```text
--id <lease-id-or-slug>   required lease identifier
--local-port <port>       local loopback port; omit or use 0 for automatic
--reverse <box>:<host>    expose host loopback port or absolute socket path <host> on the Box's <box>
--json                    with --reverse, print {"box":N,"host":N} or {"box":N,"socket":"<path>"} when ready
--provider <name>         provider selection
--network <mode>          auto, public, or tailscale target resolution
--reclaim                 claim the lease for the current repository
```

Provider and target-specific SSH flags are also accepted. The provider must
resolve an SSH target; delegated-only providers are rejected.

## Credential handling

Crabbox resolves the target internally. Secret SSH usernames, ProxyCommand
policy, key and certificate paths, and host-key settings live in a private
temporary OpenSSH config rather than the Crabbox-launched ssh argv or
environment variables. OpenSSH executes the provider-resolved ProxyCommand
under that provider's existing transport contract. Config-backed targets
materialize only route fields, so unrelated configured forwards, TTY requests,
and remote commands are not inherited. The config is removed after the forward
exits.

## See also

- [`ssh`](ssh.md) - inspect the resolved SSH command.
- [`cp`](cp.md) - copy files over native or resolved SSH transports.
- [SSH lease transport](../features/ssh-transport.md) - shared transport and
  lifecycle contract.
