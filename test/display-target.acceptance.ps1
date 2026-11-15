#Requires -Version 5.1
<#
.SYNOPSIS
Opens Chrome on each screen with the launcher's own arguments and reports where it landed.

.DESCRIPTION
Not part of CI: it opens visible windows. For each screen Windows lists (or the ones named
with -Screen) it starts Chrome on one throwaway profile with exactly the arguments the
launcher passes for that screen, reads the window back through the DevTools protocol
(bounds and window state) and from Windows (the monitor that holds it), prints one PASS,
FAIL or SKIP line, and closes the window after -HoldSeconds. Every window after the first
opens over the place Chrome saved for the one before, so a PASS there also shows that the
arguments override Chrome's saved placement. The profile is deleted at the end; the
dedicated visuals profile and every other Chrome are left alone.

.EXAMPLE
powershell -NoProfile -ExecutionPolicy Bypass -File test\display-target.acceptance.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File test\display-target.acceptance.ps1 -Screen '\\.\DISPLAY2'
#>
[CmdletBinding()]
param(
    [string] $ChromeExe = "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
    [string[]] $Screen = @(),
    [ValidateRange(1, 60)]
    [int] $HoldSeconds = 4
)
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$chrome = $ChromeExe
# Dot-sourcing sets the launcher's parameters to their defaults here.
. (Join-Path $PSScriptRoot '..\install\Start-PartyVisualsBrowser.ps1')
$ChromeExe = $chrome
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class PartyAcceptanceNative {
 [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] struct MonitorInfo {
  public int Size, Left, Top, Right, Bottom, WorkLeft, WorkTop, WorkRight, WorkBottom; public uint Flags;
  [MarshalAs(UnmanagedType.ByValTStr, SizeConst=32)] public string Device;
 }
 [DllImport("user32.dll")] static extern IntPtr MonitorFromWindow(IntPtr window, uint flags);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern bool GetMonitorInfo(IntPtr monitor, ref MonitorInfo info);
 public static string MonitorOf(IntPtr window) {
  var info = new MonitorInfo(); info.Size = Marshal.SizeOf(info);
  return GetMonitorInfo(MonitorFromWindow(window, 2), ref info) ? info.Device : "";
 }
}
'@

function Invoke-Cdp([Net.WebSockets.ClientWebSocket] $Socket, [int] $Id, [string] $Method, [hashtable] $Params = @{}) {
    $bytes = [Text.Encoding]::UTF8.GetBytes((@{ id = $Id; method = $Method; params = $Params } | ConvertTo-Json -Compress -Depth 5))
    [void]$Socket.SendAsync([ArraySegment[byte]]::new($bytes), 'Text', $true, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
    $buffer = New-Object byte[] 65536
    while ($true) {
        $text = New-Object Text.StringBuilder
        do {
            $read = $Socket.ReceiveAsync([ArraySegment[byte]]::new($buffer), [Threading.CancellationToken]::None).GetAwaiter().GetResult()
            [void]$text.Append([Text.Encoding]::UTF8.GetString($buffer, 0, $read.Count))
        } while (-not $read.EndOfMessage)
        $message = $text.ToString() | ConvertFrom-Json
        if ($message.PSObject.Properties['id'] -and $message.id -eq $Id) {
            if ($message.PSObject.Properties['error']) { throw "$Method failed: $($message.error.message)" }
            return $message.result
        }
    }
}

function Wait-For([int] $Seconds, [scriptblock] $Probe) {
    $end = [datetime]::UtcNow.AddSeconds($Seconds)
    while ([datetime]::UtcNow -lt $end) {
        $value = try { & $Probe } catch { $null }
        if ($null -ne $value) { return $value }
        Start-Sleep -Milliseconds 250
    }
    return $null
}

function Test-ScreenPlacement($Target, $Screens) {
    $placement = Add-VisualsGeometry ([pscustomobject]@{ Screen = $Target; PreferredListed = $true; Reason = 'acceptance'; Position = $null; Size = $null }) $Screens
    $name = '{0} ({1})' -f $Target.Device, (@($Target.Monitors) -join ' + ')
    if (-not $placement.Position) { return "SKIP $name`: the launcher passes no geometry here: $($placement.Reason)" }
    $portFile = Join-Path $BrowserProfile 'DevToolsActivePort'
    if (Test-Path -LiteralPath $portFile) { Remove-Item -LiteralPath $portFile }
    $launch = ((Get-BrowserArguments $placement | ForEach-Object { ConvertTo-NativeArgument $_ }) -join ' ')
    $browser = Start-Process -FilePath $ChromeExe -WorkingDirectory (Split-Path -Parent $ChromeExe) -ArgumentList $launch -PassThru
    $socket = $null
    try {
        $port = Wait-For 20 { $line = @(Get-Content -LiteralPath $portFile)[0]; if ($line -match '^\d+$') { [int]$line } }
        if ($null -eq $port) { return "FAIL $name`: Chrome wrote no DevTools port" }
        $target = Wait-For 20 { @((Invoke-RestMethod "http://127.0.0.1:$port/json/list") | Where-Object { $_.type -in @('page', 'app') })[0] }
        if ($null -eq $target) { return "FAIL $name`: Chrome showed no page" }
        $socket = New-Object Net.WebSockets.ClientWebSocket
        [void]$socket.ConnectAsync([uri](Invoke-RestMethod "http://127.0.0.1:$port/json/version").webSocketDebuggerUrl, [Threading.CancellationToken]::None).GetAwaiter().GetResult()
        $script:cdpId = 0
        $bounds = Wait-For 10 { $b = (Invoke-Cdp $socket (++$script:cdpId) 'Browser.getWindowForTarget' @{ targetId = $target.id }).bounds; if ($b.windowState -eq 'fullscreen') { $b } }
        if ($null -eq $bounds) { $bounds = (Invoke-Cdp $socket (++$script:cdpId) 'Browser.getWindowForTarget' @{ targetId = $target.id }).bounds }
        $browser.Refresh()
        $monitor = Wait-For 10 { $browser.Refresh(); if ($browser.MainWindowHandle -ne [IntPtr]::Zero) { [PartyAcceptanceNative]::MonitorOf($browser.MainWindowHandle) } }
        $landed = '{0} at {1},{2} {3}x{4} on {5}' -f $bounds.windowState, $bounds.left, $bounds.top, $bounds.width, $bounds.height, $monitor
        $expected = 'fullscreen at {0} {1} on {2}' -f $placement.Position, ($placement.Size -replace ',', 'x'), $Target.Device
        $ok = $bounds.windowState -eq 'fullscreen' -and ('{0},{1}' -f $bounds.left, $bounds.top) -eq $placement.Position -and ('{0},{1}' -f $bounds.width, $bounds.height) -eq $placement.Size -and $monitor -eq $Target.Device
        Start-Sleep -Seconds $HoldSeconds
        return ('{0} {1}: {2} (expected {3})' -f $(if ($ok) { 'PASS' } else { 'FAIL' }), $name, $landed, $expected)
    } finally {
        if ($null -ne $socket) { $socket.Dispose() }
        Close-VisualsBrowser $browser
    }
}

if (-not (Test-Path -LiteralPath $ChromeExe -PathType Leaf)) { throw "Chrome is missing: $ChromeExe" }
$BrowserProfile = Join-Path ([IO.Path]::GetTempPath()) ('party-visuals-display-acceptance-' + [guid]::NewGuid().ToString('N'))
$VisualizerUrl = 'data:text/html,<body style="background:%23103;color:%23fff;font:10vw sans-serif">Party Visuals placement test</body>'
New-Item -ItemType Directory -Path $BrowserProfile | Out-Null
$failed = 0
try {
    $screens = @(Get-VisualsScreens)
    $targets = @($screens | Where-Object { $Screen.Count -eq 0 -or $Screen -contains $_.Device })
    if ($targets.Count -eq 0) { throw "No listed screen matches: $($Screen -join ', ')" }
    foreach ($target in $targets) {
        $line = Test-ScreenPlacement $target $screens
        Write-Host $line
        if ($line.StartsWith('FAIL')) { $failed++ }
    }
} finally {
    Start-Sleep -Seconds 2
    Remove-Item -LiteralPath $BrowserProfile -Recurse -Force -ErrorAction SilentlyContinue
}
exit ([int]($failed -gt 0))
