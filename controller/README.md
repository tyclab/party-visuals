# Windows controller

The controller owns one NodeCG/browser/capture session. CLI, interactive menu,
tray and login task address that owner. Manual Off survives sleep, unlock,
sign-in and retries. Closing the menu leaves visuals running; Stop records Off
before shutdown. A closed browser leaves visuals Off until Start, and so does
a controller that starts in Manual mode with a Start left over, avoiding a guess
about whether an exit was deliberate. In Follow Party the Start stays across a
restart of the controller or of Windows: the party state decides whether
visuals run, so there is nothing to guess.

The executable reuses tycswap's public native tray adapter at the pinned Go
revision. It does not run tycswap, use its accounts or require WSL. Automatic
wake alone does not reopen the display; user resume/unlock restores requested
resources after the display settles. Shared OpenRGB is read-only: Stop never
kills it or writes a lighting mode/profile. Local actions never arm the rig,
release Freeze/Blackout or stop music.

The tray shows a rainbow TycStation Krake mark. Its source is
`cmd/party-visuals/icon/krake-rainbow.svg`; the embedded 16–64 px PNGs are
rendered from it with per-size stroke widths by headless Chrome or Chromium:
`CHROME=/path/to/chrome go generate ./cmd/party-visuals`. The tray takes the
smallest PNG that covers Windows' notification-icon size at the system scale
(16 px at 100 %, 24 px at 125–150 %, 32 px at 175–200 %), so Windows never
enlarges it; without that metric it takes 32 px, as tycswap's tray does.

```
party-visuals app
party-visuals menu
party-visuals start
party-visuals stop
party-visuals status --json
party-visuals diagnostics
party-visuals mode follow-party
party-visuals party on
party-visuals party off
party-visuals mode manual
party-visuals quit
```

`--root PATH` precedes the command for a non-default controller directory.
`diagnostics` prints the phase and the tray's diagnostic lines; `status --json`
holds everything, the Follow Party source under `followParty`.

`controller.log` in the controller directory records each tray and CLI command
(the interactive menu sends its commands through the CLI) with its source and
result, such as `command "start" from tray: accepted`, and each change of phase,
mode or intent with its reason, such as `phase attention (mode manual, Manual
Off): …`.

Follow Party is opt-in: select it in the tray or with `mode follow-party`.
Start enables following, and Stop overrides every later party event.
Armed/playing state is never inferred to be permission to start local visuals.
The party event comes from Home Assistant. The controller's command endpoint
accepts loopback calls only, so the controller pulls: while Follow Party is
selected it reads one entity (`GET /api/states/<entity>` with a bearer token)
every `partyPollMs` and sends `party on` when the state equals `partyValue`,
`party off` for any other state. It sends once per change, and once for the
first reading after each switch to Follow Party. Manual mode polls nothing.
A failed reading (error, timeout, `unavailable`, `unknown`) keeps the last
party state and retries after doubling delays of at most 60 s. Home Assistant
counts every rejected bearer toward its IP ban, so an empty token file is
refused before any request, and a 401/403 answer stops polling until the
controller restarts. `party on|off` still sets the state by hand until the
entity next changes. Each transition and failed reading is appended to
`controller.log` in the controller directory; `diagnostics`, the tray and
`status --json` show the source's state.

The source is configured by optional keys in the controller's `config.json`.
Without them nothing polls, Follow Party waits for `party on`, and diagnostics
read "Follow Party: no Home Assistant source configured".

```json
"homeAssistantUrl": "https://ha.example.invalid:8123",
"homeAssistantTokenFile": "C:\\Users\\<user>\\AppData\\Local\\PartyVisuals\\controller\\ha.token",
"partyEntity": "sensor.house_mode_code",
"partyValue": "3",
"partyPollMs": 5000
```

The URL must be https. The token file holds one Home Assistant long-lived
access token (profile, Security tab) and nothing else; a UTF-8 byte-order mark
and surrounding whitespace are ignored. Keep it in the controller directory,
which only the user and SYSTEM can read, never in `config.json` or this
repository. Before reading the file the controller reads its permissions (DACL)
and refuses the token if any entry grants access to an account other than this
Windows user, SYSTEM or Administrators: `controller.log` gets one line naming
the file and that account, and Follow Party reads `not polling: … also grants
access to <account>`. Deny entries grant nothing and do not count; a file
without a DACL counts as open to Everyone. A conditional entry (an allow entry
with a condition, such as group membership) for another account is refused too,
as `… has a conditional entry for <account>, which the controller does not
evaluate`. The account is named for well-known and built-in accounts and given
by its SID otherwise, so the check never waits for a domain controller. The
last three keys default to the values shown; `partyPollMs` takes 1000 to 60000.
`check`, and with it Stage, refuses an http URL, a relative token path, an
unreadable or empty token file, a token file other accounts can open and a
malformed entity id.

Diagnostics separate screen/helper, Lightshow analysis audio, output eligibility
and OpenRGB SDK. Analysis frames do not prove the page has audio to draw: Music
Assistant's visualizer needs audio through its player queue, and the live-audio page
draws only from the engine's live input. Stale helper status is unknown, never ready.
With Music Assistant's page, enable its player visualizer explicitly through its
normal UI; the controller never changes that preference, and when no usable canvas
exists the capture helper preserves normal MA artwork/text. The live-audio page goes
calm and names what it waits for on screen and in the helper's status file
(`display.wait`, beside `display.ageMs`). The helper's `--status-file` support is
required before a session can run.

## Choosing the screen

Without `preferredDisplay` Chrome opens the visuals where they were last closed. With
it, the worker chooses the screen at each open, whether by Start, by Follow Party or by
a reopen, and hands Chrome that screen's position and size, which override the place
Chrome saved in the profile. The visuals open fullscreen on:

1. the preferred display, while Windows lists it and it does not read off;
2. otherwise the leftmost screen that is not the primary, the upper one of two with the
   same left edge, never a preferred display that reads off;
3. otherwise the primary screen.

```json
"preferredDisplay": "<monitor name>",
"displayPowerEntity": "media_player.<tv>"
```

`preferredDisplay` is the monitor's name as Windows shows it in Settings > System >
Display > Advanced display, which comes from the monitor itself (a TV's model name,
for example), matched whole and in any letter case. A screen that mirrors another
counts when either monitor has the name; of two screens with the name, the one that
is not the primary, then the leftmost, is taken. `check` and Stage refuse a name with
surrounding spaces, control characters or more than 64 characters.

Windows can keep listing a TV while it is off or in standby, so its list alone cannot
say "on". `displayPowerEntity`, optional, names a Home Assistant entity that can: `off`
and `standby` read as off, any other state as on. It takes `homeAssistantUrl` and
`homeAssistantTokenFile` from Follow Party with their checks and rules: an empty token
file sends nothing, and a 401/403 answer stops this reading until the controller
restarts. The controller reads it every `partyPollMs` while it runs, in either mode.
The first reading counts at once; a change counts once it has held for 30 seconds,
since such an entity can read off for a few seconds while the TV stays on. A failed
reading keeps the last one. Until the first reading, and after a refused token, the
choice goes by Windows' list alone, as it does without the entity.

When the choice changes while visuals run, they reopen on the new screen. Windows
adding or removing a screen already restarts the visuals, as every display change
does; an on/off change of the power reading while Windows lists the preferred display
restarts them the same way. A window moved by hand stays where it is until the next
open, where the rule applies again; to keep the visuals where you put them, leave
`preferredDisplay` out.

Chrome takes the position and size in device-independent pixels. With one scale
factor on every screen (Settings > System > Display > Scale) the worker divides each
screen's pixels by that factor, which is exact. With different scale factors Chrome
arranges the screens outward from the primary in a way the worker does not reproduce,
so the worker hands Chrome no position, Chrome opens the visuals where they were last
closed, and the log says why.

`controller.log` records each open, such as `visuals open on \\.\DISPLAY2 (<monitor>,
3840x1600 at 3840,0): the preferred display is off; first screen other than the
primary`, and each change of the power reading. While visuals run, the tray and
`diagnostics` show the choice as `Display: …`, and with an entity its state as
`Display power: …`; `status --json` holds both under `diagnostics`.

## Keeping the controller running

The **PartyVisuals Controller** task starts the controller at sign-in and asks
again every minute. While the controller runs, Task Scheduler drops that start
(the task ignores new instances); after any exit the controller is back within
a minute. Task Scheduler's restart-on-failure covers a start that failed, not
an exit, whatever its code. A controller that ran visuals in Follow Party
resumes them once it knows there is a party: from its first reading where a
Home Assistant source is configured, from the next `party on` otherwise. In
Manual mode it comes back Off.

**Stop visuals and quit** therefore ends the controller for at most a minute:
visuals stay Off, as Stop recorded, and the tray icon returns. To keep the
controller closed, disable the task first
(`Disable-ScheduledTask -TaskName 'PartyVisuals Controller'`), then quit.

A Stop whose Off cannot be written to `intent.json` still stops the visuals,
and each further Stop answers with the error until a save works. The status
says `Could not persist Off` meanwhile, unless a worker that did not stop is
the reason it shows. In Follow Party the quit is refused as well: a controller
that ended now would come back with the Start. In Manual mode the quit goes
through, since no controller starts visuals there on its own. One that ends
with the Off unsaved, for example on Ctrl+C in a console, writes
`controller closed: Off was not saved, …` to `controller.log`.

`controller.log` says why a controller ended. One that cannot start, for
example because its configuration is refused or another controller holds the
folder, appends `controller did not start: <error>`; one that gives up later,
such as on a tray icon Windows does not take, writes
`controller stopped: <error>`. Both exit with code 1. A crash of the Go runtime
exits with code 2 and leaves its report (`panic: …` or `Exception 0x…`, then
every goroutine's stack) in the same file: the task gives the controller's
standard error no reader, so `app` points it at the log. A `controller
<version> started` line with none of these before it follows a quit, a
sign-out or a process that was ended. Only a log that cannot be opened or
written leaves an exit unexplained.

## Sign-in restore

Windows can reopen the apps that were open at shutdown or restart when the user
signs in again ("Automatically save my restartable apps and restart them when I
sign back in" in Settings > Accounts > Sign-in options, `RestartApps` under
`HKCU\Software\Microsoft\Windows NT\CurrentVersion\Winlogon`). Chrome takes
part, so a kiosk window that was open then comes back on the dedicated profile
a few seconds after sign-in, often after the controller started. Explorer
starts it outside the controller with the kiosk arguments plus
`--restore-last-session`.

Nothing the controller or the browser launcher starts carries that flag. For
the first 10 minutes after the controller starts (its task starts it at
sign-in, and again after an exit), every 3 s while it owns no session, and
once just before each Start,
it closes every Chrome browser process in this Windows session whose only
`--user-data-dir` is the configured `browserProfile` and which carries
`--restore-last-session`. When the 10 minutes end, a close still in progress
ends with them, and the next Start closes that window. Outside those sweeps the
controller does not list processes, read their command lines or end them; Stop,
sleep and the other commands do not sweep. It closes such a browser first
through its windows, so Chrome keeps the screen it was on, and after 15 s by
ending the process, as the browser launcher's `-Stop` does. Chrome on any other
profile, including the normal one, is never touched. To keep it that way,
`check`, Stage and the controller refuse a `browserProfile` that is a browser's
own profile folder: a folder named `User Data` anywhere inside
`%LOCALAPPDATA%`, such as `Google\Chrome\User Data`, `Google\Chrome Beta\User
Data`, `Chromium\User Data` or `Microsoft\Edge\User Data`, whichever browser
`chromeExe` names. Both paths are compared as Windows resolves them, so case,
trailing separators, 8.3 short names, junctions and links do not matter. The
kiosk runs on that folder with remote debugging enabled, and Stop and the sweep
close Chrome on it. Manual Off stays Off, and Start opens the controller's own
window. `controller.log` names each closed window and the tray notifies once
for each; the diagnostics line shows only the latest, such as `Browser: A
browser window restored by Windows at sign-in was closed (process N) at HH:MM`.
While one stays open, Start is refused with "A browser window restored by
Windows at sign-in is open; close it before Start." A window that could not be
ended is not tried again until the next Start, and is forgotten once it is
gone. Within the first 10 minutes, the first sweep that finds no restored
window left returns a Start refused this way to Off, or in Follow Party, which
keeps its Start across sign-in, tries it again; later the refusal stays
until the next Start, which sweeps again, or Stop. The Windows setting is left
as it is; turning it off also avoids the brief window at sign-in.

Two limits follow from matching the command line. A relative `--user-data-dir`
is resolved against the controller's working directory, and only the
`--user-data-dir=` spelling counts. Chrome's own relaunch (after an update or
`chrome://restart`) also carries `--restore-last-session`, so a dedicated-profile
window that relaunched itself is closed the same way by the next sweep while the
controller owns no session, and reported with the sign-in wording.

## Installation

### On another Windows PC

Pushing a `vX.Y.Z` tag runs the controller checks, then attaches
`party-visuals.exe`, the PowerShell 5.1 installers and `SHA256SUMS` to a draft
GitHub release, as tycswap's release workflow does. Installations see a release
only after its draft is reviewed and published. Nothing is code-signed. As the
Windows user who runs the show, in a normal (not elevated) PowerShell window:

1. Install Node.js 22 or later, Git and Chrome:
   `winget install --id OpenJS.NodeJS.LTS --exact`,
   `winget install --id Git.Git --exact`, `winget install --id Google.Chrome --exact`.
2. Install NodeCG with this bundle using `Install-PartyVisuals.ps1`
   ([Install on the show PC](../README.md#install-on-the-show-pc)) without
   `-Autostart`; the controller starts NodeCG.
3. Install the capture helper in `%LOCALAPPDATA%\PartyVisuals\browser-visuals`
   as described in the same section.
4. Run this line, with the release tag in place of `vX.Y.Z`:

```powershell
$t = 'vX.Y.Z'; $f = "$env:TEMP\Install-PartyVisualsRelease.ps1"; [Net.ServicePointManager]::SecurityProtocol = 'Tls12'; Invoke-WebRequest -UseBasicParsing "https://github.com/tyclab/party-visuals/releases/download/$t/Install-PartyVisualsRelease.ps1" -OutFile $f; powershell -NoProfile -ExecutionPolicy Bypass -File $f -Tag $t
```

It downloads the release to `%LOCALAPPDATA%\PartyVisuals\installer\<tag>` over
https only, checks every file against its one line in the release's
`SHA256SUMS`, asks for the Music Assistant address and player id (the `player=`
value of its now-playing page), names any missing prerequisite and runs Stage
with the verified hash. Add `-Cutover` to activate the controller in the same
run; visuals still begin Off and nothing is armed. `-MusicAssistantUrl`,
`-Player`, `-ChromeExe`, `-NodeExe` and `-NodecgConfig` replace the prompts and
defaults; `-LiveAudio` (release v0.1.3 or later) shows the bundle's live-audio
visualizer instead of a Music Assistant player and asks for neither
([Choosing the visualizer](../README.md#choosing-the-visualizer)); `-ConfigPath` or `-ImportLegacy` go to Stage unchanged. Curtain, room
and panel output need a reviewed `-ConfigPath`. `SHA256SUMS` comes from the same
release, so, as for tycswap's upgrade, it catches damaged or incomplete
downloads but is no signature; `-ExpectedSha256` also pins the executable to a
hash reviewed elsewhere, such as the release job log. After the first Start
with a Music Assistant player, sign in to Music Assistant once in the dedicated
browser window.

To update, run the line again with the new tag and `-Cutover`. Cutover asks the
running controller to quit, waits until it has let go of its folder, registers
the new one and starts it. Visuals begin Off, also where Follow Party had a
Start, so Start them again afterwards. A cutover that fails at its first
checks changes nothing. One that fails after it has asked the running
controller to quit leaves the controller's task disabled, as it leaves the
legacy tasks. One that fails once the new controller is registered, usually
because it did not come up Off within 30 seconds, leaves the new task
registered, and it keeps asking every minute. In each case run it again once
the cause is resolved.
The active configuration is kept. To change it,
for example to add the Home Assistant keys, edit a copy of
`%LOCALAPPDATA%\PartyVisuals\controller\config.json` and add
`-ConfigPath <copy>` to that line.

### From a checkout

Build from this directory with `go build -trimpath -o party-visuals.exe
./cmd/party-visuals`, or use the reviewed Windows CI artifact. CI builds also
carry a Windows version resource (company `tyclab`, description `Party Visuals
controller`, file and product version from the tag, `0.0.0-dev` for untagged
builds) and an application manifest: `asInvoker`, so it never asks for
elevation, per-monitor DPI aware, as the tray sets itself at run time, and
Windows 10 and 11 as supported systems. CI generates both into
`cmd/party-visuals/resource_windows_amd64.syso` from
`cmd/party-visuals/resource/` with goversioninfo, pinned as a tool in `go.mod`,
so no binary resource is committed. For the same locally, run this before
`go build`:

```
go tool goversioninfo -64 -o cmd/party-visuals/resource_windows_amd64.syso -manifest cmd/party-visuals/resource/party-visuals.exe.manifest -propagate-ver-strings -file-version 0.0.0-dev -product-version 0.0.0-dev cmd/party-visuals/resource/versioninfo.json
```

The executable stays a console program, not a `-H windowsgui` one, because the
same file is the CLI and `status`, `start`, `stop`, `quit` and the others print
to the console. Only `app` detaches: it writes its output to `controller.log`,
makes that file its standard error handle, where the Go runtime prints a crash,
and, when it is the only process on its console, as under the login task,
frees that console (FreeConsole).

In PowerShell 5.1:

```powershell
./Install-PartyVisualsController.ps1 -Action Stage `
  -ControllerExe ./party-visuals.exe -ExpectedSha256 <reviewed-sha256> -ImportLegacy
./Install-PartyVisualsController.ps1 -Action Cutover
```

Stage verifies the hash, validates candidate configuration without launching
anything, protects local state and exports owned legacy tasks. It leaves the
active configuration untouched. Stage copies the executable and the worker
scripts into one release folder (`controller\releases\<hash>`) and points
`workerScript` there. The controller runs that script with PowerShell's
execution policy bypassed, so `check`, Stage and the controller refuse a
`workerScript` that is not in the folder holding the running
`party-visuals.exe`, naming that folder. Existing visuals keep
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
Sleep also stops visuals this way. A stop that sleep interrupts finishes at
wake by ending the job. If the old worker is still exiting, the phase reads
attention ("Owned processes did not stop") and no second worker starts; the
next Start or resume after it has exited starts afresh without a controller
restart.

## Validation

Run `go test ./...`, `go test -race ./...` and `go vet ./...`. Windows CI exercises
actual file locks, Job Objects and the PowerShell worker with isolated dummy
children, plus installer rollback tests. It does not send physical outputs.

Real Chrome GPU recovery, actual sleep/wake and audible source acceptance still
need controlled deployment checks. Where Chrome puts the visuals is checked on the
show PC with `test\display-target.acceptance.ps1`: it opens Chrome on a throwaway
profile on each screen with the worker's arguments, reads the window back through the
DevTools protocol and from Windows, and prints PASS, FAIL or SKIP per screen. It
opens visible windows, so it is not part of CI. Dummy children cannot prove those. The same
holds for a real sign-in restore: CI's dummy `chrome.exe` children only show
that a dedicated-profile process carrying `--restore-last-session` is closed
through its window or ended, that other profiles are left running, and that the
worker names such a window when it refuses Start. Never
stop production NodeCG to satisfy an uninstall test: its port guard correctly
refuses removal when the port is occupied.
