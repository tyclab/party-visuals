# Windows controller

The controller owns one NodeCG/browser/capture session. CLI, interactive menu,
tray and login task address that owner. Manual Off survives sleep, unlock,
sign-in and retries. Closing the menu leaves visuals running; Stop records Off
before shutdown. A closed browser or an unclean previous owner leaves visuals
Off until Start, avoiding a guess about whether an exit was deliberate.

The executable reuses tycswap's public native tray adapter at the pinned Go
revision. It does not run tycswap, use its accounts or require WSL. Automatic
wake alone does not reopen the display; user resume/unlock restores requested
resources after the display settles. Shared OpenRGB is read-only: Stop never
kills it or writes a lighting mode/profile. Local actions never arm the rig,
release Freeze/Blackout or stop music.

The tray shows a rainbow TycStation Krake mark. Its source is
`cmd/party-visuals/icon/krake-rainbow.svg`; the embedded 16–64 px PNGs are
rendered from it with per-size stroke widths by headless Chrome or Chromium:
`CHROME=/path/to/chrome go generate ./cmd/party-visuals`.

```
party-visuals app
party-visuals menu
party-visuals start
party-visuals stop
party-visuals status --json
party-visuals mode follow-party
party-visuals party on
party-visuals party off
party-visuals mode manual
party-visuals quit
```

`--root PATH` precedes the command for a non-default controller directory.
Follow Party is opt-in. Trusted local automation sends the explicit `party`
event; Start enables following, and Stop overrides every later party event.
Armed/playing state is never inferred to be permission to start local visuals.

Diagnostics separate screen/helper, Lightshow analysis audio, output eligibility
and OpenRGB SDK. Analysis frames do not prove MA visualizer audio is supported.
Stale helper status is unknown, never ready. Enable MA's player visualizer
explicitly through its normal UI; the controller never changes that preference.
When no usable canvas exists, the updated capture helper preserves normal MA
artwork/text. Its `--status-file` support is required before a session can run.

## Installation

Build from this directory with `go build -trimpath -o party-visuals.exe
./cmd/party-visuals`, or use the reviewed Windows CI artifact. In PowerShell 5.1:

```powershell
./Install-PartyVisualsController.ps1 -Action Stage `
  -ControllerExe ./party-visuals.exe -ExpectedSha256 <reviewed-sha256> -ImportLegacy
./Install-PartyVisualsController.ps1 -Action Cutover
```

Stage verifies the hash, validates candidate configuration without launching
anything, protects local state and exports owned legacy tasks. It leaves the
active configuration untouched. Existing visuals keep
running. Actual host/player values stay in local `controller/config.json`.
An explicit `-ConfigPath` can replace legacy import. The configuration names
the existing private NodeCG configuration file; it contains no engine token.

Cutover requires exclusive controller ownership, disables the recorded
Browser/NodeCG triggers, gracefully stops those supervisors and registers the
new per-user login task. It reports active only after the new owner answers
in the Off state. Visuals begin Off. No old restart loop runs beneath
the new worker. Installation records each phase for interruption recovery.

`-Action Rollback` stops the new owner and restores previous task definitions
disabled, preserving current Manual Off. Explicitly reconcile autostart before
using a legacy launcher. Normal Chrome and shared OpenRGB remain unowned.

The local command endpoint uses a fresh credential in the current-user/SYSTEM
controller directory, separate from rig credentials. It binds an ephemeral
loopback port, rejects browser Origins/wrong Hosts and bounds requests. Lock
errors refuse a second owner. Children start only after Windows Job assignment;
shutdown first attempts graceful stop, then ends remaining owned descendants.

## Validation

Run `go test ./...`, `go test -race ./...` and `go vet ./...`. Windows CI exercises
actual file locks, Job Objects and the PowerShell worker with isolated dummy
children, plus installer rollback tests. It does not send physical outputs.

Real Chrome GPU recovery, actual sleep/wake and audible source acceptance still
need controlled deployment checks. Dummy children cannot prove those. Never
stop production NodeCG to satisfy an uninstall test: its port guard correctly
refuses removal when the port is occupied.
