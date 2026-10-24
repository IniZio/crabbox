# External Provider

Use `provider: external` when lifecycle and connection discovery belong to an
internal, proprietary, or separately versioned tool. Choose either:

- the versioned JSON protocol for adapters that need arbitrary logic; or
- declarative lifecycle commands for CLIs whose resource name and SSH target
  are deterministic.

There is no shell evaluation and no provider-specific code in Crabbox. The
external tool owns provisioning, inventory, resume, release, and private
authentication. Crabbox owns dirty-tree sync, rsync, commands, results, SSH
sessions, native VNC forwarding, and local WebVNC for desktop-capable direct
SSH leases.

## Configuration

```yaml
provider: external
target: linux
external:
  command: node
  args:
    - /absolute/path/provider.mjs
  capabilities:
    idempotentLeaseId: true
    pauseResume: true
  config:
    backend: vm
    namespace: team-devboxes
    size: cpu32
  connection:
    ssh:
      trustProviderOutput: true
  workRoot: /workspaces/crabbox
  routingFile: ""
```

When a protocol command returns SSH coordinates, put
`connection.ssh.trustProviderOutput: true` in the same trusted user config as
the exact command, arguments, `external.config`, and connection inputs.
Repository config cannot self-enable this contract, and changing the
output-producing adapter contract invalidates an inherited approval.

`workRoot` may also be relative, such as `moor`. Crabbox joins it to the login
home that the lease's own SSH session reports (`/root/moor` for user `root`,
`/home/crabbox/moor` for user `crabbox`), so an operator need not know the SSH
user in advance. `CRABBOX_WORK_ROOT` and `CRABBOX_EXTERNAL_WORK_ROOT` override
the file value. A home that cannot be read fails the lease instead of falling
back to a default root. Windows native targets still need an absolute path.

External SSH leases can target `linux`, `macos`, or `windows`. Keep `target:
linux` for ordinary devboxes. Use `target: macos` when the external adapter
returns an SSH-reachable Mac and you want Crabbox to drive native Screen Sharing
or the WebVNC portal bridge.

For macOS desktop access, Screen Sharing uses an operator-managed account on the
host; Crabbox does not provision, generate, or rotate that account's password.
Keep the password outside the config and store only an environment variable
reference:

```yaml
provider: external
target: macos
external:
  command: mac-provider-adapter
  connection:
    desktop:
      passwordEnv: SCREEN_SHARING_PASSWORD
  workRoot: /Users/developer/crabbox
```

`connection.desktop.username` is optional; Crabbox falls back to the resolved SSH
user. `passwordEnv` must be a dedicated environment variable name outside the
reserved `CRABBOX_*`, `CF_ACCESS_*`, `GIT_*`, `GH_*`, and `GITHUB_*`
namespaces and must not reuse
proxy, loader, or process-control variables such as `HTTPS_PROXY`, `LD_PRELOAD`,
`DYLD_INSERT_LIBRARIES`, `MallocDebugReport`, `GOMEMLIMIT`, `PATH`, or `HOME`.
The former documented name `CRABBOX_EXTERNAL_DESKTOP_PASSWORD` remains accepted
for compatibility, but migrate it to a dedicated non-`CRABBOX_*` name.
Treat the normalized target, Windows mode, desktop username, and password
environment name as one
credential-destination contract: define them together in trusted user config or
select them with explicit flags or environment overrides. Repository config
cannot redirect an inherited password reference to another target or account.

Inject the secret into the Crabbox process from your normal secret store; do not
put the password in YAML, argv, routing files, issue trackers, or logs. Crabbox
reads the value itself and removes that environment variable from external
adapter, declarative lifecycle, SSH, viewer, coordinator-helper, and local
inspection child processes. It is also excluded from remote run/cache
environment forwarding. Ordinary VNC output must not print this
operator-managed password; WebVNC keeps it server-side. The explicit
`crabbox vnc --native-handoff` machine-readable handoff contains the credential
needed by the native client and must be handled as secret material.

The adapter must provision native macOS Screen Sharing on target loopback
`127.0.0.1:5900`, and the configured username/password must be a valid macOS
account accepted by Apple Remote Desktop authentication—not only a legacy VNC
password. Validate the complete route without opening a viewer first:

```sh
crabbox webvnc --provider external --target macos --id <lease> --preflight
```

For native Windows SSH hosts, select `target: windows`, keep `windows.mode:
normal`, and use a drive-absolute dedicated work root such as
`C:\crabbox`. Windows drive roots, protected system directories, device names,
short-name aliases, alternate data streams, and ambiguous trailing dots or
spaces are rejected. For WSL2 hosts, select `windows.mode: wsl2` and use a POSIX
work root inside the distribution instead.

Windows target support supplies routing, shell/path semantics, SSH execution,
and lifecycle handling. Crabbox does not provision a desktop or VNC server on
an External Windows host. External is statically classified as desktop-capable;
there is no per-adapter desktop capability negotiation. A Windows adapter must
separately provide a Crabbox-compatible loopback VNC service and managed
credential file; otherwise use the target for SSH/run workflows only.

`external.config` is arbitrary YAML passed as JSON to the executable. Keep
secrets in the executable's normal credential store or environment rather than
the Crabbox config file. Generated SSH, retry, and stop commands store resolved
command, arguments, and config in a private per-lease routing file and print
only that file's opaque path. Kept acquisition failures persist this routing
before the SSH readiness wait, so the printed recovery commands can still
resolve or release the lease. An acquire that fails before routing is saved
releases the lease even with `--keep`, unless a lease ID was requested. Routing
files use mode `0600` and are removed after successful release. Persistence
flushes the private temporary file before
rename, then flushes the installed directory and its complete ancestor chain;
a sync failure is reported and a retry repeats the entire durability chain.
Confirmed-absence cleanup likewise flushes the containing directories after
removing matching routing, claim, and slug-reservation sidecars. A failed
directory flush keeps controller cleanup pending, and the next attempt repeats
the flush even when the sidecar was already removed.

`external.capabilities.idempotentLeaseId` is an explicit adapter contract, not
a protocol-v1 default. Enable it only when repeated `acquire` requests for one
fixed `desired.leaseId`, `desired.slug`, and `desired.name` always return that
same resource. `adapter serve` rejects external adapters without this
opt-in before any lifecycle side effect. Declarative adapters must also use
the command-attested identity contract described below.

`external.capabilities.pauseResume` enables the `pause` and `resume` protocol
operations behind `crabbox pause` and `crabbox resume`. Set it only in trusted
user config; repository config, environment variables, and flags cannot enable
it. Without it, both commands fail with exit 2 and
`provider=external does not support pause`. It has no effect with a declarative
`external.lifecycle`, which has no pause or resume operation. It does not change
the local claim scope.

`external.capabilities.cacheVolumes` lets Crabbox hand configured
[cache volumes](../features/cache-volumes.md) to the adapter on `acquire`. Like
`pauseResume`, set it only in trusted user config; repository config,
environment variables, and flags cannot enable it. With it, `crabbox providers
--json --configured` lists `cache-volume` for `external`; without it, configured
volumes are not sent and a `required` volume fails early with
`provider=external does not support required cache volume`. It has no effect with
a declarative `external.lifecycle`, and it does not change the local claim scope.

Local claims are scoped to a fingerprint of the selected protocol command or
declarative lifecycle, connection templates, `external.config`, normalized
target, and normalized Windows mode. For backward compatibility, the normalized
default Linux case adds no target fields to the encoded scope, preserving its
legacy hash and ownership of existing claims and routing files. This lets
multiple external backends, namespaces, targets, or Windows modes reuse the
same slug without cleanup for one configuration removing claims or routing
files owned by another.
Legacy unscoped claims are not reconciled by cleanup; stop them directly by
lease ID or with the generated routing file.

An explicit requested slug is a fixed provider identity: Crabbox either reserves
that exact normalized slug or fails the acquisition. It never silently appends
a collision suffix. Generated slugs may still receive a suffix. Reservations
are published by fsyncing a private temporary file, atomically renaming it, and
syncing the reservation directory. Creation persists every newly created
ancestor entry and directory in order, so a crash cannot strand only part of
the reservation path. Retries repeat the complete chain even when an earlier
attempt left newly created directories visible after a failed sync; stale and
same-attempt recovery also syncs directory removals before reuse. Reservations
record the owner PID plus process
start identity and, on Linux, the kernel boot ID. A retry reclaims a fresh same-attempt reservation immediately
when that exact owner has exited or the PID was recycled; it never waits the
generic six-hour collision TTL. A PID/start-tick pair from an earlier Linux
boot is never treated as a live owner. A still-running exact owner and any mismatched
lease/slug/token identity remain protected.

## Declarative lifecycle

Use declarative lifecycle commands when the provider CLI can create a
preselected resource name and expose that resource through a deterministic SSH
alias or hostname:

```yaml
provider: external
target: linux
external:
  lifecycle:
    doctor:
      argv: [devboxctl, list, --format, json]
    acquire:
      steps:
        - [devboxctl, new, "{{resourceName}}", --size, "{{config.size}}"]
        - [devboxctl, setup, "{{resourceName}}"]
      rollbackOnFailure: true
      env:
        DEVBOX_TOKEN: "{{env.DEVBOX_TOKEN}}"
    resolve:
      argv: [devboxctl, inspect, "{{resourceName}}"]
    list:
      argv: [devboxctl, list, --format, json]
      output: json-name-array
      namePrefix: "cbx-"
    release:
      argv: [devboxctl, rm, --yes, "{{resourceName}}"]
    touch:
      argv: [devboxctl, touch, "{{resourceName}}"]
    cleanup:
      argv: [devboxctl, gc]
  connection:
    resourceName: "{{leaseIdSlug}}"
    cloudId: devboxes/{{resourceName}}
    serverType: "{{config.size}}"
    labels:
      backend: container
    ssh:
      user: "{{env.DEVBOX_USER}}"
      host: "{{resourceName}}"
      port: "22"
      allowEnv: true
      trustProviderOutput: true
      sshConfigProxy: true
      readyCheck: command -v git && command -v rsync && command -v tar
  config:
    size: cpu16
  workRoot: /home/developer/crabbox
```

Place this destination-bearing example in trusted user config. If repository
config owns the lifecycle, repeat the exact `resourceName`, SSH destination,
and environment opt-in in trusted user config before using operator-managed
SSH credentials.

`acquire`, `list`, `release`, and `connection.ssh.user` are required.
Lifecycle operations configure exactly one of `argv` or `steps`. Steps run in
order and stop at the first failure. For structured output, only the final
step's stdout is parsed; earlier stdout is forwarded as diagnostic output.
`acquire.rollbackOnFailure: true` runs the configured release operation when a
later acquire step fails after at least one successful step, unless the caller
requested `--keep`.

`connection.resourceName` defaults to `{{name}}`; use it when the provider has
stricter resource-name limits than Crabbox. Crabbox stores the resolved value
with the lease so `json-name-array` inventory can recover the original lease
ID and slug. `connection.ssh.host` defaults to `{{resourceName}}`. Optional
operations are skipped when absent; Crabbox still maintains local touch
metadata.

Each `argv` or `steps` item is passed directly to the configured executable.
Shell operators, pipes, variable expansion, and command substitution are not
evaluated. Supported placeholders:

```text
{{leaseId}} {{leaseIdSlug}} {{slug}} {{name}} {{resourceName}} {{id}} {{state}}
{{keep}} {{reclaim}} {{releaseOnly}} {{force}}
{{all}} {{refresh}} {{dryRun}}
{{repo.root}} {{repo.name}} {{repo.remoteUrl}} {{repo.head}} {{repo.baseRef}}
{{config.<scalar-key>}}
{{env.<NAME>}}
```

Environment placeholders require the named variable to be set. Values expanded
from `{{env.<NAME>}}` are rejected in lifecycle `argv` and `steps` by default
because process arguments may be visible to other local processes. Pass secrets
through an operation `env:` map instead; those entries are added to the child
process environment without being copied into argv:

```yaml
external:
  lifecycle:
    acquire:
      argv: [devboxctl, new, "{{resourceName}}"]
      env:
        DEVBOX_TOKEN: "{{env.DEVBOX_TOKEN}}"
```

For non-secret environment-backed arguments, set `allowEnvArgv: true` on that
operation. For non-secret environment-backed resource names, also set
`connection.allowEnvResourceName: true`; Crabbox records that provenance so a
later release still requires `allowEnvArgv` before placing `{{resourceName}}`
back in argv. Do not use either opt-in for tokens, passwords, API keys, or
other credential contents.

Repository lifecycle commands also cannot place `{{config.*}}` values inherited
from user config, an explicit config file, or `--external-config-json` in
`argv` or `steps`. Pass credentials through the operation `env:` map. For an
inherited non-secret value that must be an argument, set `allowConfigArgv: true`
on that operation and repeat the exact full lifecycle plus `resourceName` and
`cloudId` template contract in trusted user config. The repository cannot grant
itself this opt-in, and changing any lifecycle command, argument, environment
mapping, output mode, cleanup behavior, or config-derived connection template
invalidates the approval. Repository-owned `external.config` values remain
ordinary project automation.

Environment-derived expansion in `connection.ssh` fields is rejected by
default, including indirect use through an environment-backed `resourceName`,
because values such as the SSH user, host, key path, ready check, or proxy
command can expose process environment data to a network destination or local
process. For non-secret environment-backed SSH routing values, set
`connection.ssh.allowEnv: true` in trusted user config. Repository config
cannot grant itself this opt-in. The opt-in does not approve inherited SSH
authentication for a repository-selected destination.

A repository-selected External SSH `host`, default-host `resourceName`, or
`proxyCommand` is rejected even when the repository supplies a key. OpenSSH
can still add identities from operator user config, and a nested SSH proxy can
authenticate independently of the outer key. To approve the destination,
repeat the exact SSH endpoint templates (user, host, key, port, fallback ports,
and proxy settings) plus any `resourceName` they reference in trusted user
config (see
[Configuration](../features/configuration.md#precedence)).
Templates that reference `repo.*` inputs remain repository-selected. Templates
that reference `config.*` are approved only when the effective
`external.config` map also comes from trusted config.

`leaseIdSlug` is the lease ID normalized as a lowercase slug, suitable for
providers that require DNS-style names. `resourceName` is the expanded
`connection.resourceName`.

`acquire.output` and `resolve.output` accept `json-lease`. The final command
must print one plain JSON lease object (not the protocol response wrapper) with
nonempty `leaseId`, `slug`, `name`, and provider-immutable `cloudId` fields.
Acquire and normal resolve output must also include the SSH fields needed to
connect. For example:

```json
{"leaseId":"cbx_0123456789ab","slug":"fast-coral","name":"crabbox-fast-coral-deadbeef","cloudId":"provider/resource-123","ssh":{"user":"dev","host":"devbox-fast-coral","port":"22"}}
```

The three Crabbox identity fields must exactly echo the requested
`leaseId`, `slug`, and `name`; the provider-native identity belongs only in
`cloudId`. Identity values must be trimmed, printable, and at most 4096 bytes;
`leaseId` must be the requested canonical `cbx_...` ID and `slug` must already
be normalized. A compact controller-ready configuration looks like:

```yaml
external:
  capabilities:
    idempotentLeaseId: true
  lifecycle:
    acquire:
      argv: [provider-adapter, acquire, "{{leaseId}}", "{{slug}}", "{{name}}", "{{resourceName}}"]
      output: json-lease
    resolve:
      argv: [provider-adapter, resolve, "{{leaseId}}", "{{slug}}", "{{name}}", "{{resourceName}}"]
      output: json-lease
    list:
      argv: [provider-adapter, list]
      output: json-lease-array
    release:
      argv: [provider-adapter, release, "{{leaseId}}", "{{slug}}", "{{name}}", "{{cloudId}}"]
  connection:
    resourceName: "{{leaseIdSlug}}"
    ssh:
      user: developer
      trustProviderOutput: true
```

Because `json-lease` and `json-lease-array` may supply SSH coordinates directly,
their `trustProviderOutput` contract and the complete lifecycle/configuration
that produces the output must be approved together in trusted user config.
Without that explicit source-bound approval, Crabbox rejects returned SSH
coordinates before connecting or persisting validated routing state.

A declarative adapter qualifies for controller fixed-ID provisioning only when
`idempotentLeaseId` is true, both acquire and resolve use `json-lease`, list
uses `json-lease-array`, and every configured release command has a standalone
argument exactly equal to `{{cloudId}}`. Every inventory item must contain the
same four identity fields. Each release helper must use the raw `cloudId`
argument as its resource
constraint. This binds release to the raw
identity returned by the provider command; default connection expansion and
`json-name-array` synthesis do not qualify. Raw lease output may not set the
reserved `lease`, `slug`, `name`, `externalResourceName`, or
`externalResourceNameFromEnv` labels.

`list.output` accepts:

- `json-name-array`: stdout is a JSON array of resource names;
- `json-lease-array`: stdout is a JSON array using the protocol lease shape
  documented below. Controller-capable declarative adapters require nonempty
  `leaseId`, `slug`, `name`, and `cloudId` fields in every item; ordinary
  legacy declarative adapters retain the existing partial lease-array format.

For `json-name-array`, optional `list.namePrefix` discards inventory names
outside the expanded prefix before Crabbox constructs leases. Use it when a
provider CLI lists resources that are not all owned by this configuration.
Both list formats select SSH destinations and therefore require the same
source-bound `connection.ssh.trustProviderOutput` contract.

Declarative configuration and resolved connection templates are included in
the private per-lease routing file. This lets generated retry, daemon, SSH, and
stop commands work without the original config file.

Routing files use the OS user-config directory by default. A non-empty
`XDG_CONFIG_HOME` takes precedence on every supported platform, including
macOS, and places them below `$XDG_CONFIG_HOME/crabbox/external/`. The override
must be an absolute path without surrounding whitespace; Crabbox rejects an
invalid value instead of silently falling back to another directory.

Flags:

```text
--external-command
--external-arg
--external-config-json
--external-idempotent-lease-id
--external-work-root
--external-routing-file
--external-desktop-username
--external-desktop-password-env
```

Environment:

```text
CRABBOX_EXTERNAL_COMMAND
CRABBOX_EXTERNAL_ARG
CRABBOX_EXTERNAL_IDEMPOTENT_LEASE_ID
CRABBOX_EXTERNAL_WORK_ROOT
CRABBOX_EXTERNAL_ROUTING_FILE
CRABBOX_EXTERNAL_DESKTOP_USERNAME
CRABBOX_EXTERNAL_DESKTOP_PASSWORD_ENV
```

The repository live harness can exercise a configured external provider:

```sh
CRABBOX_LIVE=1 \
CRABBOX_LIVE_COORDINATOR=0 \
CRABBOX_LIVE_PROVIDERS=external \
scripts/live-smoke.sh
```

The provider may come from declarative lifecycle configuration or
`external.command` in the Crabbox config. Set
`CRABBOX_LIVE_EXTERNAL_COMMAND=/absolute/path/provider` to override the
protocol command.
`CRABBOX_LIVE_EXTERNAL_ARG` adds one command argument through
`CRABBOX_EXTERNAL_ARG` for quick local smoke runs; use config `external.args`
for repeatable multi-argument setups.
`CRABBOX_LIVE_COMMAND` overrides the command executed inside the leased box.
The live harness also needs `jq` and `rg` on the operator machine.

## Protocol

Protocol version 1 uses JSON over stdio:

- stdin: exactly one request object;
- stdout: exactly one response object;
- stderr: diagnostics for the user;
- exit nonzero: operation failure.

Request shape:

```json
{
  "protocolVersion": 1,
  "operation": "acquire",
  "config": {"backend": "vm"},
  "desired": {
    "leaseId": "cbx_0123456789ab",
    "slug": "fast-coral",
    "name": "crabbox-fast-coral-deadbeef",
    "class": "beast"
  },
  "keep": false,
  "reclaim": false,
  "repo": {
    "root": "/workspace/my-app",
    "name": "my-app",
    "remoteUrl": "https://github.com/example-org/my-app.git",
    "head": "0123456789abcdef",
    "baseRef": "main"
  }
}
```

Controller-driven release-only `resolve` and `release` requests also include an
optional immutable expectation:

```json
{
  "expected": {
    "leaseId": "cbx_0123456789ab",
    "attemptLeaseId": "cbx_0123456789ab",
    "slug": "fast-coral",
    "cloudId": "private-control-plane/devbox-fast-coral"
  }
}
```

Adapters must use this as a match constraint, not as permission to retarget a
different resource. For a controller release-only resolve, the resolver's raw
response must explicitly repeat every nonempty expected lease ID, attempt ID,
slug, and `cloudId`. Omitted fields are rejected before desired-value or
protocol-compatibility filling; a declarative default assembled from the
request cannot authorize release. Crabbox independently validates the complete
resolved target again before release.

Operations:

```text
doctor
acquire
resolve
list
release
touch
cleanup
pause   (only with external.capabilities.pauseResume)
resume  (only with external.capabilities.pauseResume)
```

`pause` and `resume` first send a `resolve` request for the lease with
`releaseOnly: true`, so the adapter must answer it for a paused lease without
waiting for SSH. Crabbox then sends the
operation with the resolved lease:

```json
{
  "protocolVersion": 1,
  "operation": "pause",
  "config": {"backend": "vm"},
  "lease": {
    "leaseId": "cbx_0123456789ab",
    "slug": "fast-coral",
    "name": "devbox-fast-coral",
    "cloudId": "private-control-plane/devbox-fast-coral"
  }
}
```

The response is `{"protocolVersion":1}` with an optional `message`. A `resume`
response may also return a `lease` with `ssh.hostKey`, which replaces the lease's
host-key pin. An
`{"error":"..."}` response or a nonzero exit fails the Crabbox command with a
nonzero exit and includes the tail of the adapter's stderr. When the capability
is enabled, `crabbox providers --json` lists `pause-resume` for `external`;
otherwise it does not.

Cache volumes ride on `acquire` only, and only with
`external.capabilities.cacheVolumes`. Each entry names a `group`, the adapter
store the volume lives in, plus a `name`, an opaque `key`, one or more `paths`,
and an optional `sizeGB` and `required`. A path is `~/<rel>` (the lease's login
home) or an absolute path. Every field is omitted when empty, and
`protocolVersion` stays 1:

```json
{
  "protocolVersion": 1,
  "operation": "acquire",
  "desired": {
    "leaseId": "cbx_0123456789ab",
    "slug": "fast-coral",
    "name": "devbox-fast-coral",
    "cacheVolumes": [
      {"group": "7f3a9c1e04b2d655", "name": "gradle", "key": "7f3a9c1e04b2d655-gradle",
       "paths": ["~/.gradle/caches", "~/.gradle/wrapper"]},
      {"group": "7f3a9c1e04b2d655", "name": "android-sdk", "key": "7f3a9c1e04b2d655-android-sdk",
       "paths": ["/opt/android-sdk"], "sizeGB": 12}
    ]
  }
}
```

`group` and `key` must match `[A-Za-z0-9._-]{1,96}`.

When `CRABBOX_CACHE_SCOPE` is set and the request carries `cacheVolumes`,
`desired` also gets `"cacheScope": "<16 lowercase hex digits>"`, the tenant
scope the adapter uses to partition its stores. Crabbox refuses any other
value at startup with `CRABBOX_CACHE_SCOPE must be 16 lowercase hex digits`.
The field is omitted when empty or when no volumes are sent, and it does not
change the local claim scope.

The lease in the response reports what the adapter did with each volume:

```json
{"protocolVersion": 1, "lease": {"...": "...", "cacheVolumes": [
  {"name": "gradle", "outcome": "restored", "detail": "gen 1729781203"},
  {"name": "android-sdk", "outcome": "empty", "detail": "no generation yet"}
]}}
```

`outcome` is `restored`, `empty`, or `skipped`. Crabbox prints each entry on
stderr as `cache volume NAME: OUTCOME (detail)`. When a volume marked `required`
comes back `skipped`, Crabbox rolls the acquisition back through `release` and
fails. A skipped volume that is not required leaves a plain directory and the
lease proceeds.

Successful lease response:

```json
{
  "protocolVersion": 1,
  "lease": {
    "leaseId": "cbx_0123456789ab",
    "slug": "fast-coral",
    "name": "devbox-fast-coral",
    "cloudId": "private-control-plane/devbox-fast-coral",
    "status": "ready",
    "serverType": "cpu32",
    "labels": {"region": "example"},
    "ssh": {
      "user": "dev",
      "host": "devbox-fast-coral",
      "port": "22",
      "key": "/absolute/path/id_ed25519",
      "hostKey": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5...",
      "sshConfigProxy": true,
      "proxyCommand": "private-cli proxy devbox-fast-coral %p",
      "readyCheck": "command -v git && command -v rsync && command -v tar"
    }
  }
}
```

When `resolve` returns `status` of `paused`, `stopped`, or `released` (any
case), Crabbox stops at once with exit code 4 and a `crabbox stop --id <id>`
hint, unless the request is `releaseOnly` or `statusOnly`. Other statuses, such
as `starting`, still wait for SSH.

`sshConfigProxy: true` supports an SSH alias already installed in the user's
SSH config. `proxyCommand` supports control-plane tunnels directly. Either path
is automatically reused by rsync and WebVNC. A non-empty `proxyCommand` implies
proxy-backed SSH even if `sshConfigProxy` is omitted.

`desired.class` carries the value of `--class <name>` and is omitted when no
class was given. Adapters may map it to a machine size.

`ssh.hostKey` is optional. It is one OpenSSH public host key line, such as
`ssh-ed25519 AAAA...`, without a hostname. When present, Crabbox pins it for
the lease: SSH, rsync, and tunnels use `StrictHostKeyChecking=yes` against a
per-lease known_hosts file and ignore the user's and the global known_hosts.
Every `acquire`, `resolve`, or `resume` that returns a `hostKey` replaces the pin, so a
key that changes after the VM restarts still connects. If a lease was pinned
and a later response omits `hostKey`, Crabbox keeps the old pin, and a changed
host key fails closed. Adapters that return no `hostKey` behave as before.

When `readyCheck` is omitted, Crabbox uses a generic Linux tool check for
`bash`, `python3`, `git`, `rsync`, and `tar`. Return an explicit `readyCheck`
when the external provider needs a stronger guest bootstrap signal or targets a
non-Linux host.
`crabbox tunnel` needs only a working SSH transport, so its resolve waits for
`exit 0` instead of the ready check, and it does not probe the same endpoint
again after claiming the lease.

`list` returns `{"protocolVersion":1,"leases":[...]}`. Protocol-command adapters
that enable `idempotentLeaseId` must return a real array, including `[]` for an
empty inventory, and every row must contain canonical `leaseId`, normalized
`slug`, exact `name`, and immutable `cloudId`; omitted, partial, or `null`
inventory cannot prove controller absence. `doctor` may return a human-readable
`message`. Any operation may return `{"error":"..."}`; error-only responses do
not need `protocolVersion`.

For ordinary protocol-command `acquire`, omitted `leaseId`, `slug`, or `name`
fields inherit the corresponding values from `desired`. A fixed-ID protocol
adapter that enables `idempotentLeaseId` must instead return the complete raw
`leaseId`, `slug`, `name`, and `cloudId` tuple. Declarative `json-lease` output
never receives compatibility filling.

Adapters that enable `idempotentLeaseId` must treat repeated `acquire` requests
with the same `desired.leaseId`, `desired.slug`, and `desired.name` as
idempotent. Controllers preserve that attempt identity across crash recovery
and may retry it after a full absence-reconciliation window; the retry must
return the same external resource rather than create another. Controller-capable
fixed-attempt responses must return the complete raw identity tuple and every
value must match `desired` exactly.

`leaseId` is Crabbox's local lease identity. For new `acquire` responses and
non-release `resolve` responses, it must be the generated `cbx_...` value from
`desired`. External systems should put their own resource identifier in
`cloudId`; Crabbox persists claims and stop routing by `leaseId`. Release-only
paths still accept older protocol-v1 provider IDs so existing leases can be
stopped, but path-shaped legacy IDs are not used for local claim deletion.
Controller stop additionally supplies its full persisted lease ID, attempt ID,
slug, and `cloudId` expectation. Release-only resolution and release both
validate every nonempty expected field before the adapter's destructive
operation runs, including attempts canceled before a local claim was written.
