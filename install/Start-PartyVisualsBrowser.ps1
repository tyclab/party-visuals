#Requires -Version 5.1
[CmdletBinding()]
param(
    [string] $ChromeExe = "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
    [string] $NodeExe = "$env:ProgramFiles\nodejs\node.exe",
    [string] $BrowserProfile = "$env:LOCALAPPDATA\PartyVisuals\browser-profile",
    [string] $HelperPath = "$env:LOCALAPPDATA\PartyVisuals\browser-visuals\cli.js",
    [string] $VisualizerUrl = 'http://10.27.2.42:8095/#/now-playing?player=02%3A01%3Abb%3A12%3A81%3A49&frameless=1',
    [string] $ControlsUrl = 'http://127.0.0.1:9090/bundles/party-visuals/api/state',
    [switch] $Curtain,
    [string] $NodecgConfig = "$env:LOCALAPPDATA\PartyVisuals\EclipseGraphics\cfg\party-visuals.json",
    [ValidateRange(1, 65535)]
    [int] $Fixture = 53
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

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
    if ($Curtain) {
        '--curtain'; '--nodecg-config'; $NodecgConfig
        '--fixture'; [string]$Fixture
        '--width'; '68'; '--height'; '42'; '--fps'; '10'; '--fit'; 'cover'
    }
}

function Get-ProfileBrowser {
    $session = (Get-Process -Id $PID).SessionId
    $profile = [IO.Path]::GetFullPath($BrowserProfile).TrimEnd('\')
    $found = @(foreach ($candidate in Get-CimInstance Win32_Process -Filter "Name='chrome.exe'") {
        if ($candidate.SessionId -ne $session -or -not $candidate.CommandLine) { continue }
        $args = [PartyBrowserNative]::Arguments($candidate.CommandLine)
        $dirs = @($args | Where-Object { $_.StartsWith('--user-data-dir=', [StringComparison]::OrdinalIgnoreCase) })
        if ($dirs.Count -ne 1) { continue }
        $directory = [IO.Path]::GetFullPath($dirs[0].Substring(16)).TrimEnd('\')
        if ($directory -ine $profile -or @($args | Where-Object { $_ -like '--type=*' }).Count -gt 0) { continue }
        if ($args -notcontains '--remote-debugging-port=0' -or $args -notcontains '--remote-debugging-address=127.0.0.1' -or $args -notcontains ('--app=' + $VisualizerUrl)) {
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

function Start-VisualsBrowser {
    foreach ($path in @($ChromeExe, $NodeExe, $HelperPath)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Required executable or helper is missing: $path" }
    }
    if ($Curtain -and -not (Test-Path -LiteralPath $NodecgConfig -PathType Leaf)) { throw 'The curtain configuration is missing.' }
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
        $browser = Get-ProfileBrowser
        if ($null -eq $browser) {
            New-Item -ItemType Directory -Path $BrowserProfile -Force | Out-Null
            $portFile = Join-Path $BrowserProfile 'DevToolsActivePort'
            if (Test-Path -LiteralPath $portFile) { Remove-Item -LiteralPath $portFile }
            $args = ((Get-BrowserArguments | ForEach-Object { ConvertTo-NativeArgument $_ }) -join ' ')
            $browser = Start-Process -FilePath $ChromeExe -WorkingDirectory (Split-Path -Parent $ChromeExe) -ArgumentList $args -PassThru
        }
        Write-Host 'The visuals browser is open; move it to any screen (F11 toggles fullscreen).'
        # Closing the window ends the task; run it again to reopen.
        while (-not $browser.HasExited) {
            if ($null -ne $helper -and $helper.HasExited) {
                Stop-BrowserHelper $helper
                $helper = $null
                Start-Sleep -Seconds 3
            }
            if ($null -eq $helper) { $helper = Start-BrowserHelper }
            Start-Sleep -Seconds 1
        }
        return 0
    } finally {
        Stop-BrowserHelper $helper
        if ($owned) { $mutex.ReleaseMutex() }
        $mutex.Dispose()
    }
}

if ($MyInvocation.InvocationName -eq '.') { return }
exit (Start-VisualsBrowser)
