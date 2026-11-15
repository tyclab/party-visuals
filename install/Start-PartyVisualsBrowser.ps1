#Requires -Version 5.1
[CmdletBinding()]
param(
    [string] $ChromeExe = "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
    [string] $NodeExe = "$env:ProgramFiles\nodejs\node.exe",
    [string] $BrowserProfile = "$env:LOCALAPPDATA\PartyVisuals\browser-profile",
    [string] $HelperPath = "$env:LOCALAPPDATA\PartyVisuals\browser-visuals\cli.js",
    [string] $VisualizerUrl = '',
    [string] $ControlsUrl = 'http://127.0.0.1:9090/bundles/party-visuals/api/state',
    [string] $TelemetryFile = "$env:LOCALAPPDATA\PartyVisuals\logs\browser-visuals.ndjson",
    [string] $StatusFile = '',
    [switch] $Curtain,
    [switch] $Room,
    [ValidatePattern('^(?:[0-9]+:[0-9]+x[0-9]+)(?:,[0-9]+:[0-9]+x[0-9]+)*$|^$')]
    [string] $Panels = '',
    [ValidateRange(0, 60)]
    [int] $Fps = 0,
    [string] $NodecgConfig = "$env:LOCALAPPDATA\PartyVisuals\EclipseGraphics\cfg\party-visuals.json",
    [ValidateRange(1, 65535)]
    [int] $Fixture = 53,
    [string] $StopFile = "$env:LOCALAPPDATA\PartyVisuals\browser-visuals.stop",
    [string] $PreferredDisplay = '',
    [string] $TaskName = 'PartyVisuals Browser',
    [switch] $Stop,
    [switch] $Resume,
    [switch] $Register
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$LauncherPath = $PSCommandPath
$LaunchParameters = $PSBoundParameters

function Initialize-BrowserNative {
    if ('PartyBrowserNative' -as [type]) { return }
    Add-Type @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
public static class PartyBrowserNative {
 public class Screen { public string Device; public string[] Monitors; public int X, Y, Width, Height, Dpi; public bool Primary; }
 [StructLayout(LayoutKind.Sequential)] public struct Luid { public uint Low; public int High; }
 [StructLayout(LayoutKind.Sequential)] public struct PathInfo {
  public Luid SourceAdapter; public uint SourceId, SourceMode, SourceFlags;
  public Luid TargetAdapter; public uint TargetId, TargetMode, Technology, Rotation, Scaling, RefreshNumerator, RefreshDenominator, ScanLineOrdering; public int TargetAvailable; public uint TargetFlags;
  public uint Flags;
 }
 [StructLayout(LayoutKind.Sequential, Size=64)] public struct ModeInfo { public uint Type; }
 [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] public struct SourceName {
  public uint Type, Size; public Luid Adapter; public uint Id;
  [MarshalAs(UnmanagedType.ByValTStr, SizeConst=32)] public string Device;
 }
 [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] public struct TargetName {
  public uint Type, Size; public Luid Adapter; public uint Id, Flags, Technology; public ushort Maker, Product; public uint Connector;
  [MarshalAs(UnmanagedType.ByValTStr, SizeConst=64)] public string Monitor;
  [MarshalAs(UnmanagedType.ByValTStr, SizeConst=128)] public string Path;
 }
 [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] public struct MonitorInfo {
  public int Size, Left, Top, Right, Bottom, WorkLeft, WorkTop, WorkRight, WorkBottom; public uint Flags;
  [MarshalAs(UnmanagedType.ByValTStr, SizeConst=32)] public string Device;
 }
 delegate bool MonitorVisitor(IntPtr monitor, IntPtr dc, IntPtr rect, IntPtr data);
 [DllImport("user32.dll")] static extern int GetDisplayConfigBufferSizes(uint flags, out uint paths, out uint modes);
 [DllImport("user32.dll")] static extern int QueryDisplayConfig(uint flags, ref uint pathCount, [Out] PathInfo[] paths, ref uint modeCount, [Out] ModeInfo[] modes, IntPtr topology);
 [DllImport("user32.dll")] static extern int DisplayConfigGetDeviceInfo(ref SourceName name);
 [DllImport("user32.dll")] static extern int DisplayConfigGetDeviceInfo(ref TargetName name);
 [DllImport("user32.dll")] static extern bool EnumDisplayMonitors(IntPtr dc, IntPtr clip, MonitorVisitor visit, IntPtr data);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern bool GetMonitorInfo(IntPtr monitor, ref MonitorInfo info);
 [DllImport("shcore.dll")] static extern int GetDpiForMonitor(IntPtr monitor, int kind, out uint x, out uint y);
 [DllImport("user32.dll")] static extern IntPtr SetThreadDpiAwarenessContext(IntPtr context);
 static Dictionary<string, List<string>> MonitorNames() {
  var names = new Dictionary<string, List<string>>(StringComparer.OrdinalIgnoreCase);
  uint pathCount, modeCount; PathInfo[] paths; int result;
  do {
   if (GetDisplayConfigBufferSizes(2, out pathCount, out modeCount) != 0) return names;
   paths = new PathInfo[pathCount];
   result = QueryDisplayConfig(2, ref pathCount, paths, ref modeCount, new ModeInfo[modeCount], IntPtr.Zero);
  } while (result == 122);
  if (result != 0) return names;
  for (int i = 0; i < pathCount; i++) {
   var source = new SourceName(); source.Type = 1; source.Size = (uint)Marshal.SizeOf(source); source.Adapter = paths[i].SourceAdapter; source.Id = paths[i].SourceId;
   var target = new TargetName(); target.Type = 2; target.Size = (uint)Marshal.SizeOf(target); target.Adapter = paths[i].TargetAdapter; target.Id = paths[i].TargetId;
   if (DisplayConfigGetDeviceInfo(ref source) != 0 || DisplayConfigGetDeviceInfo(ref target) != 0) continue;
   if (!names.ContainsKey(source.Device)) names[source.Device] = new List<string>();
   if (!String.IsNullOrEmpty(target.Monitor)) names[source.Device].Add(target.Monitor);
  }
  return names;
 }
 public static Screen[] Screens() {
  var names = MonitorNames();
  var found = new List<Screen>();
  var previous = IntPtr.Zero;
  try { previous = SetThreadDpiAwarenessContext(new IntPtr(-4)); } catch (EntryPointNotFoundException) { }
  try {
   EnumDisplayMonitors(IntPtr.Zero, IntPtr.Zero, delegate(IntPtr monitor, IntPtr dc, IntPtr rect, IntPtr data) {
    var info = new MonitorInfo(); info.Size = Marshal.SizeOf(info);
    if (!GetMonitorInfo(monitor, ref info)) return true;
    uint dpi = 0, unused;
    if (previous == IntPtr.Zero || GetDpiForMonitor(monitor, 0, out dpi, out unused) != 0) dpi = 0;
    List<string> monitors;
    found.Add(new Screen { Device = info.Device, Monitors = names.TryGetValue(info.Device, out monitors) ? monitors.ToArray() : new string[0], X = info.Left, Y = info.Top, Width = info.Right - info.Left, Height = info.Bottom - info.Top, Dpi = (int)dpi, Primary = (info.Flags & 1) != 0 });
    return true;
   }, IntPtr.Zero);
  } finally { if (previous != IntPtr.Zero) SetThreadDpiAwarenessContext(previous); }
  return found.ToArray();
 }
 [DllImport("shell32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern IntPtr CommandLineToArgvW(string command, out int count);
 [DllImport("kernel32.dll")] static extern IntPtr LocalFree(IntPtr memory);
 public static string[] Arguments(string command) {
  int count; var memory = CommandLineToArgvW(command, out count);
  if (memory == IntPtr.Zero) return new string[0];
  try {
   var args = new string[count];
   for (int i = 0; i < count; i++) args[i] = Marshal.PtrToStringUni(Marshal.ReadIntPtr(memory, i * IntPtr.Size));
   return args;
  } finally { LocalFree(memory); }
 }
 [DllImport("wtsapi32.dll", SetLastError=true)] static extern bool WTSQuerySessionInformationW(IntPtr server, int session, int infoClass, out IntPtr buffer, out int bytes);
 [DllImport("wtsapi32.dll")] static extern void WTSFreeMemory(IntPtr memory);
 [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] public struct SessionInfo {
  public int State, Id, InBytes, OutBytes, InFrames, OutFrames, InCompressed, OutCompressed;
  [MarshalAs(UnmanagedType.ByValTStr, SizeConst=32)] public string Station;
  [MarshalAs(UnmanagedType.ByValTStr, SizeConst=17)] public string Domain;
  [MarshalAs(UnmanagedType.ByValTStr, SizeConst=21)] public string User;
  public long ConnectTime, DisconnectTime, LastInputTime, LogonTime, CurrentTime;
 }
 public static object Session(int id) {
  IntPtr memory; int bytes;
  if (!WTSQuerySessionInformationW(IntPtr.Zero, id, 24, out memory, out bytes)) return null;
  try { return Marshal.PtrToStructure(memory, typeof(SessionInfo)); } finally { WTSFreeMemory(memory); }
 }
}
'@
}

function ConvertTo-NativeArgument([string] $Value) {
    if ($Value -match '[\r\n\x00]') { throw 'Process arguments cannot contain line breaks or NUL.' }
    return '"' + [regex]::Replace([regex]::Replace($Value, '(\\*)"', '$1$1\"'), '(\\+)$', '$1$1') + '"'
}

# Without a placement Chrome reopens the app window where it was last left; a placement's
# position and size, in Chrome's device-independent pixels, override that saved place.
function Get-BrowserArguments($Placement = $null) {
    @('--user-data-dir=' + $BrowserProfile)
    '--remote-debugging-address=127.0.0.1'
    '--remote-debugging-port=0'
    '--disable-backgrounding-occluded-windows'
    '--disable-renderer-backgrounding'
    '--disable-background-timer-throttling'
    '--no-first-run'
    '--no-default-browser-check'
    '--start-fullscreen'
    if ($null -ne $Placement -and $Placement.Position) { '--window-position=' + $Placement.Position; '--window-size=' + $Placement.Size }
    '--app=' + $VisualizerUrl
}

function Get-VisualsScreens {
    Initialize-BrowserNative
    @([PartyBrowserNative]::Screens())
}

# The preferred display unless it reads off; else the leftmost, then topmost, screen that is
# not the primary; else the primary. Power: none (no reading configured), on, off or unknown.
function Select-VisualsScreen($Screens, [string] $Preferred, [ValidateSet('none', 'on', 'off', 'unknown')][string] $Power) {
    $all = @($Screens | Where-Object { $null -ne $_ })
    $ordered = @($all | Sort-Object @{ Expression = { [bool]$_.Primary } }, @{ Expression = { [int]$_.X } }, @{ Expression = { [int]$_.Y } })
    $named = @($ordered | Where-Object { @($_.Monitors) -contains $Preferred })
    $placement = [pscustomobject]@{ Screen = $null; PreferredListed = $named.Count -gt 0; Reason = 'Windows listed no screen'; Position = $null; Size = $null }
    if ($all.Count -eq 0) { return $placement }
    if ($named.Count -and $Power -ne 'off') {
        $placement.Screen = $named[0]
        $placement.Reason = 'the preferred display ' + @{ none = 'is connected'; on = 'is connected and on'; unknown = 'is connected; its power is not known' }[$Power]
    } else {
        $why = if ($named.Count) { 'the preferred display is off' } else { 'the preferred display is not connected' }
        $others = @($ordered | Where-Object { -not $_.Primary -and $named -notcontains $_ })
        if ($others.Count) { $placement.Screen = $others[0]; $placement.Reason = $why + '; first screen other than the primary' }
        else { $placement.Screen = @(@($ordered | Where-Object { $_.Primary }) + $ordered)[0]; $placement.Reason = $why + '; no screen other than the primary' }
    }
    return Add-VisualsGeometry $placement $all
}

# Sets the placement's position and size in Chrome's device-independent pixels, or says why not.
function Add-VisualsGeometry($Placement, $Screens) {
    $dpis = @($Screens | ForEach-Object { [int]$_.Dpi } | Sort-Object -Unique)
    $kept = ', so Chrome opens the window where it was last left'
    if ($dpis -contains 0) { $Placement.Reason += "; Windows did not report every screen's scale factor$kept"; return $Placement }
    # Chrome lays out screens of different scale factors outward from the primary; only one shared factor maps exactly.
    if ($dpis.Count -gt 1) { $Placement.Reason += '; screens have different scale factors (' + (@($dpis | ForEach-Object { '{0} %' -f [int][Math]::Round($_ * 100 / 96) }) -join ', ') + ")$kept"; return $Placement }
    $scale = $dpis[0] / 96
    $screen = $Placement.Screen
    $left = [int][Math]::Floor($screen.X / $scale)
    $top = [int][Math]::Floor($screen.Y / $scale)
    $Placement.Position = '{0},{1}' -f $left, $top
    $Placement.Size = '{0},{1}' -f ([int][Math]::Ceiling(($screen.X + $screen.Width) / $scale) - $left), ([int][Math]::Ceiling(($screen.Y + $screen.Height) / $scale) - $top)
    return $Placement
}

function Format-VisualsPlacement($Placement) {
    $screen = $Placement.Screen
    if ($null -eq $screen) { return $Placement.Reason }
    $monitors = if (@($screen.Monitors).Count) { @($screen.Monitors) -join ' + ' } else { 'unnamed monitor' }
    '{0} ({1}, {2}x{3} at {4},{5}): {6}' -f $screen.Device, $monitors, $screen.Width, $screen.Height, $screen.X, $screen.Y, $Placement.Reason
}

function Get-HelperArguments {
    $HelperPath
    '--browser-profile'; $BrowserProfile
    '--visualizer-url'; $VisualizerUrl
    '--controls-url'; $ControlsUrl
    '--parent-pid'; [string]$PID
    if ($StatusFile) { '--status-file'; $StatusFile }
    if ($Curtain -or $Room -or $Panels) { '--telemetry-file'; $TelemetryFile }
    if ($Curtain) {
        '--curtain'; '--nodecg-config'; $NodecgConfig
        '--fixture'; [string]$Fixture
        '--width'; '68'; '--height'; '42'; '--fit'; 'cover'
    }
    if ($Room) {
        # All output connections read the same configuration file.
        '--room'
        if (-not $Curtain) { '--nodecg-config'; $NodecgConfig }
    }
    if ($Panels) {
        '--panels'; $Panels
        if (-not $Curtain -and -not $Room) { '--nodecg-config'; $NodecgConfig }
    }
    if (($Curtain -or $Room -or $Panels) -and $Fps -gt 0) { '--fps'; [string]$Fps }
}

function Get-ProfileBrowser {
    $session = (Get-Process -Id $PID).SessionId
    $profile = [IO.Path]::GetFullPath($BrowserProfile).TrimEnd('\')
    $found = @(foreach ($candidate in Get-CimInstance Win32_Process -Filter "Name='chrome.exe'") {
        if ($candidate.SessionId -ne $session -or -not $candidate.CommandLine) { continue }
        $candidateArgs = [PartyBrowserNative]::Arguments($candidate.CommandLine)
        $dirs = @($candidateArgs | Where-Object { $_.StartsWith('--user-data-dir=', [StringComparison]::OrdinalIgnoreCase) })
        if ($dirs.Count -ne 1) { continue }
        $directory = [IO.Path]::GetFullPath($dirs[0].Substring(16)).TrimEnd('\')
        if ($directory -ine $profile -or @($candidateArgs | Where-Object { $_ -like '--type=*' }).Count -gt 0) { continue }
        $captureFlags = @('--disable-backgrounding-occluded-windows', '--disable-renderer-backgrounding', '--disable-background-timer-throttling')
        if ($candidateArgs -notcontains '--remote-debugging-port=0' -or $candidateArgs -notcontains '--remote-debugging-address=127.0.0.1' -or $candidateArgs -notcontains ('--app=' + $VisualizerUrl) -or @($captureFlags | Where-Object { $candidateArgs -notcontains $_ }).Count) {
            throw 'The dedicated browser profile is already open with different launch options. Close that profile before starting this task.'
        }
        # Windows adds --restore-last-session when it restarts Chrome at sign-in; no launcher passes it.
        $restored = @($candidateArgs | Where-Object { $_ -ieq '--restore-last-session' }).Count -gt 0
        Get-Process -Id $candidate.ProcessId -ErrorAction SilentlyContinue | Add-Member -NotePropertyName RestoredAtSignIn -NotePropertyValue $restored -PassThru
    })
    if ($found.Count -gt 1) { throw 'Multiple browsers own the dedicated profile; refusing another launch.' }
    if ($found.Count -eq 1) { return $found[0] }
    return $null
}

function Start-BrowserHelper {
    $info = New-Object Diagnostics.ProcessStartInfo
    $info.FileName = $NodeExe
    $info.WorkingDirectory = Split-Path -Parent $HelperPath
    $info.Arguments = ((Get-HelperArguments | ForEach-Object { ConvertTo-NativeArgument $_ }) -join ' ')
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardInput = $true
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $info
    if (-not $process.Start()) { throw 'Could not start the local browser helper.' }
    return $process
}

function Stop-BrowserHelper($Process) {
    if ($null -eq $Process) { return }
    try {
        if (-not $Process.HasExited) {
            try { $Process.StandardInput.WriteLine('stop'); $Process.StandardInput.Flush() } catch { }
            if (-not $Process.WaitForExit(5000)) {
                $Process.Kill()
                if (-not $Process.WaitForExit(2000)) { throw 'The previous browser helper did not stop; refusing another instance.' }
            }
        }
    } finally { $Process.Dispose() }
}

function Get-DisplaySession {
    $info = [PartyBrowserNative]::Session((Get-Process -Id $PID).SessionId)
    if ($null -eq $info) { return [pscustomobject]@{ Active = $true; Station = ''; ConnectedAt = [datetime]::MinValue; LogonAt = [datetime]::MinValue } }
    [pscustomobject]@{ Active = $info.State -eq 0; Station = [string]$info.Station; ConnectedAt = [datetime]::FromFileTimeUtc($info.ConnectTime); LogonAt = [datetime]::FromFileTimeUtc($info.LogonTime) }
}

function Test-StopRequested($Session) {
    $file = [IO.FileInfo]::new($StopFile)
    if (-not $file.Exists) { return $false }
    # A stop holds for the sign-in it was made in, so a restart or a new sign-in brings the visuals back.
    if ($file.LastWriteTimeUtc -ge $Session.LogonAt) { return $true }
    try { $file.Delete() } catch { }
    return $false
}

function Get-RelaunchDelay([int] $Previous, [timespan] $Uptime) {
    # A browser that stayed up for a minute has recovered; one that keeps closing at once is retried less often.
    if ($Uptime.TotalSeconds -ge 60) { return 5 }
    return [Math]::Min(300, [Math]::Max(5, 2 * $Previous))
}

function Close-VisualsBrowser($Browser) {
    if ($null -eq $Browser -or $Browser.HasExited) { return }
    # Closing the window rather than the process lets Chrome save the screen the operator put it on.
    [void]$Browser.CloseMainWindow()
    if ($Browser.WaitForExit(15000)) { return }
    try { $Browser.Kill() } catch [InvalidOperationException] { }
    [void]$Browser.WaitForExit(5000)
}

function Open-VisualsBrowser([string] $Power = 'none') {
    $script:VisualsPlacement = $null
    $browser = Get-ProfileBrowser
    if ($null -eq $browser) {
        if ($PreferredDisplay) { $script:VisualsPlacement = Select-VisualsScreen (Get-VisualsScreens) $PreferredDisplay $Power }
        New-Item -ItemType Directory -Path $BrowserProfile -Force | Out-Null
        $portFile = Join-Path $BrowserProfile 'DevToolsActivePort'
        if (Test-Path -LiteralPath $portFile) { Remove-Item -LiteralPath $portFile }
        $launch = ((Get-BrowserArguments $script:VisualsPlacement | ForEach-Object { ConvertTo-NativeArgument $_ }) -join ' ')
        $browser = Start-Process -FilePath $ChromeExe -WorkingDirectory (Split-Path -Parent $ChromeExe) -ArgumentList $launch -PassThru
    }
    # A held handle keeps an adopted browser's exit visible after Windows reuses its process ID.
    $null = $browser.Handle
    return $browser
}

function Assert-BrowserConfiguration {
    foreach ($path in @($ChromeExe, $NodeExe, $HelperPath)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Required executable or helper is missing: $path" }
    }
    if (($Curtain -or $Room -or $Panels) -and -not (Test-Path -LiteralPath $NodecgConfig -PathType Leaf)) { throw 'The engine output configuration is missing.' }
    $profile = [IO.Path]::GetFullPath($BrowserProfile).TrimEnd('\')
    $normalProfile = [IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'Google\Chrome\User Data')).TrimEnd('\')
    if ($profile -ieq $normalProfile -or $profile.StartsWith($normalProfile + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'A separate PartyVisuals browser profile is required; the normal Chrome profile is not supported.' }
    $visualizer = [uri]$VisualizerUrl
    $controls = [uri]$ControlsUrl
    if ($visualizer.Scheme -notin @('http', 'https') -or $visualizer.UserInfo -or $controls.Scheme -ne 'http' -or $controls.Host -ne '127.0.0.1' -or $controls.UserInfo) { throw 'Use a plain visualizer URL and loopback HTTP controls URL, without credentials.' }
}

function Start-VisualsBrowser {
    Assert-BrowserConfiguration
    Initialize-BrowserNative
    $mutex = New-Object Threading.Mutex($false, 'Local\PartyVisuals-Browser-Launch')
    $owned = $false
    $helper = $null
    try {
        try { $owned = $mutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $owned = $true }
        if (-not $owned) { return 0 }
        $browser = $null
        $startedAt = [datetime]::MinValue
        $station = ''
        $delay = 0
        $wait = 0
        $shown = $false
        while ($true) {
            $session = Get-DisplaySession
            if (Test-StopRequested $session) { Close-VisualsBrowser $browser; return 0 }
            # A Chrome started for another display renders slowly after the session moves; a fresh one gets the screen's rate.
            $moved = $session.Active -and ([datetime]::UtcNow - $session.ConnectedAt).TotalSeconds -ge 10 -and ($session.ConnectedAt -gt $startedAt -or $session.Station -ne $station)
            if ($null -ne $browser -and -not $browser.HasExited -and $moved) {
                Write-Host 'The session moved to another display; reopening the visuals browser there.'
                Close-VisualsBrowser $browser
                $browser = $null
                $wait = 0
            }
            if ($null -ne $browser -and $browser.HasExited) {
                $delay = Get-RelaunchDelay $delay ([datetime]::UtcNow - $startedAt)
                $wait = $delay
                $browser = $null
                Write-Host "The visuals browser closed; reopening it in $delay seconds (-Stop keeps it closed)."
            }
            if ($null -eq $browser -and $wait -gt 0) { $wait-- }
            elseif ($null -eq $browser) {
                $browser = Open-VisualsBrowser
                $startedAt = $browser.StartTime.ToUniversalTime()
                $station = $session.Station
                if ($null -ne $script:VisualsPlacement) { Write-Host ('The visuals browser opens on ' + (Format-VisualsPlacement $script:VisualsPlacement)) }
                elseif (-not $shown) { Write-Host 'The visuals browser is open; move it to any screen (F11 toggles fullscreen).' }
                $shown = $true
            }
            if ($null -ne $helper -and $helper.HasExited) {
                Stop-BrowserHelper $helper
                $helper = $null
                Start-Sleep -Seconds 3
            }
            if ($null -eq $helper) { $helper = Start-BrowserHelper }
            Start-Sleep -Seconds 1
        }
    } finally {
        Stop-BrowserHelper $helper
        if ($owned) { $mutex.ReleaseMutex() }
        $mutex.Dispose()
    }
}

function Request-VisualsStop {
    New-Item -ItemType Directory -Path (Split-Path -Parent $StopFile) -Force | Out-Null
    [IO.File]::WriteAllText($StopFile, "Visuals stopped on purpose; -Resume or the next sign-in starts them again.`r`n")
    Initialize-BrowserNative
    Close-VisualsBrowser (Get-ProfileBrowser)
    Write-Host 'The visuals browser stays closed until -Resume or the next sign-in.'
    return 0
}

function Request-VisualsResume {
    if ([IO.File]::Exists($StopFile)) { [IO.File]::Delete($StopFile) }
    Start-ScheduledTask -TaskName $TaskName
    Write-Host "Started '$TaskName'."
    return 0
}

function Get-TaskArguments($Bound) {
    '-NoProfile'; '-ExecutionPolicy'; 'Bypass'; '-WindowStyle'; 'Hidden'; '-File'; (ConvertTo-NativeArgument $LauncherPath)
    foreach ($name in @($Bound.Keys | Sort-Object)) {
        if ($name -in @('Stop', 'Resume', 'Register', 'TaskName') -or [Management.Automation.Cmdlet]::CommonParameters -contains $name) { continue }
        $value = $Bound[$name]
        if ($value -is [Management.Automation.SwitchParameter]) { if ($value.IsPresent) { '-' + $name } }
        else { '-' + $name; ConvertTo-NativeArgument ([string]$value) }
    }
}

function Get-TaskXml([string] $User, [string] $Arguments) {
    $who = [Security.SecurityElement]::Escape($User)
    $sessionTriggers = foreach ($change in @('ConsoleConnect', 'RemoteConnect', 'SessionUnlock')) {
        "    <SessionStateChangeTrigger><StateChange>$change</StateChange><UserId>$who</UserId></SessionStateChangeTrigger>"
    }
    $shell = [Security.SecurityElement]::Escape("$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe")
    $line = [Security.SecurityElement]::Escape($Arguments)
    $folder = [Security.SecurityElement]::Escape((Split-Path -Parent $LauncherPath))
    # Without a priority the task starts below normal, and Chrome's browser, audio and network processes inherit that.
    @"
<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>Keeps the Music Assistant visualizer open in the dedicated Chrome profile (party-visuals Start-PartyVisualsBrowser.ps1)</Description></RegistrationInfo>
  <Triggers>
    <LogonTrigger><UserId>$who</UserId></LogonTrigger>
$($sessionTriggers -join "`r`n")
  </Triggers>
  <Principals><Principal id="Author"><UserId>$who</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>5</Priority>
    <RestartOnFailure><Interval>PT1M</Interval><Count>999</Count></RestartOnFailure>
  </Settings>
  <Actions Context="Author"><Exec><Command>$shell</Command><Arguments>$line</Arguments><WorkingDirectory>$folder</WorkingDirectory></Exec></Actions>
</Task>
"@
}

function Register-VisualsTask($Bound) {
    $user = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    Register-ScheduledTask -TaskName $TaskName -Xml (Get-TaskXml $user ((Get-TaskArguments $Bound) -join ' ')) -Force | Out-Null
    Write-Host "Registered '$TaskName'. Stop and start it once to run this launcher now."
    return 0
}

function Invoke-BrowserLauncher {
    if (@(@($Stop, $Resume, $Register) | Where-Object { $_ }).Count -gt 1) { throw 'Use only one of -Stop, -Resume and -Register.' }
    if ($Stop) { return Request-VisualsStop }
    if ($Resume) { return Request-VisualsResume }
    if (-not $VisualizerUrl) { throw 'Pass -VisualizerUrl or set visualizerUrl in the controller configuration.' }
    if ($Register) { return Register-VisualsTask $LaunchParameters }
    return Start-VisualsBrowser
}

if ($MyInvocation.InvocationName -eq '.') { return }
exit (Invoke-BrowserLauncher)
