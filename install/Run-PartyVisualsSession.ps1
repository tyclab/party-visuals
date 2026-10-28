#Requires -Version 5.1
[CmdletBinding()]
param([Parameter(Mandatory = $true)][string] $ConfigPath)
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

# The controller assigns this process to its Job before permitting any children.
if ([Console]::ReadLine() -ne 'start') { exit 0 }
$config = Get-Content -LiteralPath $ConfigPath -Raw | ConvertFrom-Json
if ($config.version -ne 1) { throw 'Unsupported worker configuration.' }
. (Join-Path $PSScriptRoot 'Start-PartyVisualsNodecg.ps1')
. (Join-Path $PSScriptRoot 'Start-PartyVisualsBrowser.ps1')
$NodeExe = $config.nodeExe
$ChromeExe = $config.chromeExe
$NodecgRoot = $config.nodecgRoot
$BrowserProfile = $config.browserProfile
$HelperPath = $config.helperPath
$VisualizerUrl = $config.visualizerUrl
$ControlsUrl = $config.controlsUrl
$NodecgConfig = $config.nodecgConfig
$TelemetryFile = $config.telemetryFile
$StatusFile = $config.statusFile
$Curtain = [bool]$config.curtain
$Room = [bool]$config.room
$Panels = [string]$config.panels
$Fixture = [int]$config.fixture
$Fps = [int]$config.fps
$nodecg = $null; $browser = $null; $helper = $null
$nodeOwned = $false; $browserOwned = $false
Initialize-NodecgNative
Initialize-BrowserNative
$log = New-Object PartyNodecgLog($config.logFile, 1MB)
$nodeMutex = New-Object Threading.Mutex($false, (Get-LauncherMutexName))
$browserMutex = New-Object Threading.Mutex($false, 'Local\PartyVisuals-Browser-Launch')
try {
    try { $nodeOwned = $nodeMutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $nodeOwned = $true }
    try { $browserOwned = $browserMutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $browserOwned = $true }
    if (-not $nodeOwned -or -not $browserOwned) { throw 'A previous launcher still owns visuals; finish controller cutover before Start.' }
    Assert-BrowserConfiguration
    if ($null -ne (Get-ProfileBrowser)) { throw 'The dedicated browser is already open outside this session; close it before Start.' }
    $controls = [uri]$ControlsUrl
    $listeners = @(Get-NetTCPConnection -LocalPort $controls.Port -State Listen -ErrorAction SilentlyContinue)
    if ($listeners.Count) { throw 'NodeCG port is already in use outside this controller; no second instance was started.' }
    $nodecg = Start-NodecgProcess $log
    $browser = Open-VisualsBrowser
    $helper = Start-BrowserHelper
    [Console]::WriteLine('{"event":"ready"}')
    $inputLine = [Console]::In.ReadLineAsync()
    while ($true) {
        if ($inputLine.IsCompleted) { break }
        if ($browser.HasExited) { [Console]::WriteLine('{"event":"browser-ended"}'); break }
        if ($helper.HasExited) { throw 'Capture helper ended; inspect diagnostics before starting again.' }
        if ($nodecg.Process.HasExited) { throw 'NodeCG ended; inspect its local log before starting again.' }
        Start-Sleep -Milliseconds 200
    }
} catch {
    [Console]::WriteLine((@{ event = 'error'; reason = $_.Exception.Message } | ConvertTo-Json -Compress))
    exit 1
} finally {
    Stop-BrowserHelper $helper
    Close-VisualsBrowser $browser
    Stop-NodecgProcess $nodecg $log
    if ($browserOwned) { $browserMutex.ReleaseMutex() }
    if ($nodeOwned) { $nodeMutex.ReleaseMutex() }
    $browserMutex.Dispose(); $nodeMutex.Dispose()
}
