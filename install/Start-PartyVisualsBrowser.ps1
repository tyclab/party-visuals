#Requires -Version 5.1
[CmdletBinding()]
param(
    [string] $ChromeExe = "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
    [string] $NodeExe = "$env:ProgramFiles\nodejs\node.exe",
    [string] $BrowserProfile = "$env:LOCALAPPDATA\PartyVisuals\browser-profile",
    [string] $HelperPath = "$env:LOCALAPPDATA\PartyVisuals\browser-visuals\cli.js",
    [string] $VisualizerUrl = 'http://10.27.2.42:8095/#/now-playing?player=02%3A01%3Abb%3A12%3A81%3A49&frameless=1',
    [string] $ControlsUrl = 'http://127.0.0.1:9090/bundles/party-visuals/api/state',
    [string] $TelemetryFile = "$env:LOCALAPPDATA\PartyVisuals\logs\browser-visuals.ndjson",
    [switch] $Curtain,
    [switch] $Room,
    [ValidateRange(0, 60)]
    [int] $Fps = 0,
    [string] $NodecgConfig = "$env:LOCALAPPDATA\PartyVisuals\EclipseGraphics\cfg\party-visuals.json",
    [ValidateRange(1, 65535)]
    [int] $Fixture = 53,
    [string] $StopFile = "$env:LOCALAPPDATA\PartyVisuals\browser-visuals.stop",
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
using System.Runtime.InteropServices;
public static class PartyBrowserNative {
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

# No window geometry: Chrome reopens the app window on the screen where it was last left.
function Get-BrowserArguments {
    @('--user-data-dir=' + $BrowserProfile)
    '--remote-debugging-address=127.0.0.1'
    '--remote-debugging-port=0'
    '--disable-backgrounding-occluded-windows'
    '--disable-renderer-backgrounding'
    '--disable-background-timer-throttling'
    '--no-first-run'
    '--no-default-browser-check'
    '--start-fullscreen'
    '--app=' + $VisualizerUrl
}

function Get-HelperArguments {
    $HelperPath
    '--browser-profile'; $BrowserProfile
    '--visualizer-url'; $VisualizerUrl
    '--controls-url'; $ControlsUrl
    '--parent-pid'; [string]$PID
    if ($Curtain -or $Room) { '--telemetry-file'; $TelemetryFile }
    if ($Curtain) {
        '--curtain'; '--nodecg-config'; $NodecgConfig
        '--fixture'; [string]$Fixture
        '--width'; '68'; '--height'; '42'; '--fit'; 'cover'
    }
    if ($Room) {
        # The room picture rides the same lightshow connection; the configuration path is given once.
        '--room'
        if (-not $Curtain) { '--nodecg-config'; $NodecgConfig }
    }
    if (($Curtain -or $Room) -and $Fps -gt 0) { '--fps'; [string]$Fps }
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
        Get-Process -Id $candidate.ProcessId -ErrorAction SilentlyContinue
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

function Open-VisualsBrowser {
    $browser = Get-ProfileBrowser
    if ($null -eq $browser) {
        New-Item -ItemType Directory -Path $BrowserProfile -Force | Out-Null
        $portFile = Join-Path $BrowserProfile 'DevToolsActivePort'
        if (Test-Path -LiteralPath $portFile) { Remove-Item -LiteralPath $portFile }
        $launch = ((Get-BrowserArguments | ForEach-Object { ConvertTo-NativeArgument $_ }) -join ' ')
        $browser = Start-Process -FilePath $ChromeExe -WorkingDirectory (Split-Path -Parent $ChromeExe) -ArgumentList $launch -PassThru
    }
    # A held handle keeps an adopted browser's exit visible after Windows reuses its process ID.
    $null = $browser.Handle
    return $browser
}

function Start-VisualsBrowser {
    foreach ($path in @($ChromeExe, $NodeExe, $HelperPath)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Required executable or helper is missing: $path" }
    }
    if (($Curtain -or $Room) -and -not (Test-Path -LiteralPath $NodecgConfig -PathType Leaf)) { throw 'The lightshow output configuration is missing.' }
    $profile = [IO.Path]::GetFullPath($BrowserProfile).TrimEnd('\')
    $normalProfile = [IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'Google\Chrome\User Data')).TrimEnd('\')
    if ($profile -ieq $normalProfile -or $profile.StartsWith($normalProfile + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'A separate PartyVisuals browser profile is required; the normal Chrome profile is not supported.' }
    $visualizer = [uri]$VisualizerUrl
    $controls = [uri]$ControlsUrl
    if ($visualizer.Scheme -notin @('http', 'https') -or $visualizer.UserInfo -or $controls.Scheme -ne 'http' -or $controls.Host -ne '127.0.0.1' -or $controls.UserInfo) { throw 'Use a plain visualizer URL and loopback HTTP controls URL, without credentials.' }
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
                if (-not $shown) { Write-Host 'The visuals browser is open; move it to any screen (F11 toggles fullscreen).' }
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
    if ($Register) { return Register-VisualsTask $LaunchParameters }
    return Start-VisualsBrowser
}

if ($MyInvocation.InvocationName -eq '.') { return }
exit (Invoke-BrowserLauncher)
