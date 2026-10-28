#Requires -Version 5.1
<#
.SYNOPSIS
Downloads a party-visuals release, verifies every file against its SHA256SUMS
and stages the Windows controller with Install-PartyVisualsController.ps1.
-Cutover also activates it; visuals begin Off either way.

.EXAMPLE
powershell -NoProfile -ExecutionPolicy Bypass -File .\Install-PartyVisualsRelease.ps1 -Tag v0.1.0 -MusicAssistantUrl http://music.example:8095 -Player 02:00:00:00:00:01
#>
[CmdletBinding()]
param(
    [ValidatePattern('^v\d+\.\d+\.\d+[0-9A-Za-z.-]*$|^$')][string] $Tag = '',
    [string] $Root = "$env:LOCALAPPDATA\PartyVisuals",
    [ValidatePattern('^[a-fA-F0-9]{64}$|^$')][string] $ExpectedSha256 = '',
    [string] $MusicAssistantUrl,
    [string] $Player,
    [string] $ChromeExe,
    [string] $NodeExe,
    [string] $NodecgConfig,
    [string] $ConfigPath,
    [switch] $ImportLegacy,
    [switch] $Cutover
)
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$ReleaseBase = 'https://github.com/tyclab/party-visuals/releases/download'
$Assets = @('party-visuals.exe','Install-PartyVisualsController.ps1','Run-PartyVisualsSession.ps1','Start-PartyVisualsBrowser.ps1','Start-PartyVisualsNodecg.ps1')
$Usage = 'Usage: Install-PartyVisualsRelease.ps1 -Tag vX.Y.Z [-MusicAssistantUrl http://host:8095 -Player <id> | -ConfigPath <file> | -ImportLegacy] [-ExpectedSha256 <sha256>] [-Cutover]'
$WindowsPowerShell = "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe"

function Save-ReleaseAsset([string] $Name, [string] $Path) {
    $response = Invoke-WebRequest -UseBasicParsing -Uri "$ReleaseBase/$Tag/$Name" -OutFile $Path -PassThru
    Assert-HttpsResponse $response $Name
}
function Assert-HttpsResponse($Response, [string] $Name) {
    $base = $Response.BaseResponse
    $final = if ($base.PSObject.Properties['ResponseUri']) { $base.ResponseUri } else { $base.RequestMessage.RequestUri }
    if ($null -eq $final -or $final.Scheme -ne 'https') { throw "$Name of release $Tag did not arrive over https; refusing it." }
}
function Read-ReleaseSums([string] $Path) {
    if ((Get-Item -LiteralPath $Path).Length -gt 1MB) { throw "SHA256SUMS of release $Tag is larger than 1 MiB." }
    $sums = @{}
    foreach ($line in [IO.File]::ReadAllLines($Path)) {
        if (-not $line.Trim()) { continue }
        if ($line -notmatch '^([0-9a-fA-F]{64}) [ *](\S+)$') { throw "SHA256SUMS of release $Tag has an unreadable line." }
        if ($sums.ContainsKey($Matches[2])) { throw "SHA256SUMS of release $Tag has more than one line for $($Matches[2])." }
        $sums[$Matches[2]] = $Matches[1].ToLowerInvariant()
    }
    return $sums
}
function Get-VerifiedRelease([string] $Directory) {
    if (Test-Path -LiteralPath $Directory) {
        if ((Get-Item -LiteralPath $Directory -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "$Directory is a link; nothing was downloaded." }
        Remove-Item -LiteralPath $Directory -Recurse -Force
    }
    New-Item -ItemType Directory -Path $Directory -Force | Out-Null
    try {
        $sumsPath = Join-Path $Directory 'SHA256SUMS'
        Save-ReleaseAsset 'SHA256SUMS' $sumsPath
        $sums = Read-ReleaseSums $sumsPath
        foreach ($name in $Assets) {
            if (-not $sums.ContainsKey($name)) { throw "Release $Tag lists no checksum for $name." }
            $path = Join-Path $Directory $name
            Save-ReleaseAsset $name $path
            $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
            if ($hash -ne $sums[$name]) { throw "Checksum mismatch for $name in release $Tag." }
        }
        $exeHash = $sums['party-visuals.exe']
        if ($ExpectedSha256 -and $exeHash -ne $ExpectedSha256.ToLowerInvariant()) { throw "party-visuals.exe in release $Tag does not match -ExpectedSha256." }
        return $exeHash
    } catch {
        Remove-Item -LiteralPath $Directory -Recurse -Force -ErrorAction SilentlyContinue
        throw
    }
}
function Get-MusicAssistantBase([string] $Url) {
    $uri = $null
    if (-not [Uri]::TryCreate($Url, [UriKind]::Absolute, [ref] $uri) -or ($uri.Scheme -ne 'http' -and $uri.Scheme -ne 'https')) { throw 'Music Assistant address must be http(s)://host:port.' }
    if ($uri.UserInfo -or $uri.Query -or $uri.Fragment) { throw 'Music Assistant address must not carry credentials, a query or a fragment.' }
    return $uri.GetLeftPart([UriPartial]::Authority) + $uri.AbsolutePath.TrimEnd('/')
}
function Find-Chrome {
    foreach ($candidate in @("$env:ProgramFiles\Google\Chrome\Application\chrome.exe", "${env:ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe", "$env:LOCALAPPDATA\Google\Chrome\Application\chrome.exe")) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
    }
    return "$env:ProgramFiles\Google\Chrome\Application\chrome.exe"
}
function Find-Node {
    $command = Get-Command node.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -ne $command) { return $command.Path }
    return "$env:ProgramFiles\nodejs\node.exe"
}
function New-ControllerConfig([string] $MusicAssistantUrl, [string] $Player, [string] $ChromeExe, [string] $NodeExe, [string] $NodecgConfig) {
    $base = Get-MusicAssistantBase $MusicAssistantUrl
    if (-not $Player.Trim() -or $Player -match '[\x00-\x1f]') { throw 'A Music Assistant player id is required.' }
    $logs = Join-Path $Root 'logs'
    [ordered]@{
        version = 1; workerScript = ''; nodeExe = $NodeExe; chromeExe = $ChromeExe
        nodecgRoot = (Join-Path $Root 'EclipseGraphics'); browserProfile = (Join-Path $Root 'browser-profile')
        helperPath = (Join-Path $Root 'browser-visuals\cli.js')
        visualizerUrl = $base + '/#/now-playing?player=' + [Uri]::EscapeDataString($Player.Trim()) + '&frameless=1'
        controlsUrl = 'http://127.0.0.1:9090/bundles/party-visuals/api/state'; nodecgConfig = $NodecgConfig
        telemetryFile = (Join-Path $logs 'browser-visuals.ndjson'); statusFile = (Join-Path $logs 'controller-helper.json')
        logFile = (Join-Path $logs 'nodecg.log'); curtain = $false; room = $false; panels = ''; fixture = 53; fps = 0
    }
}
function Assert-Prerequisites($Config) {
    $missing = @()
    if (-not (Test-Path -LiteralPath $Config.nodeExe -PathType Leaf)) { $missing += "Node.js 22 or later ($($Config.nodeExe)): winget install --id OpenJS.NodeJS.LTS --exact" }
    if (-not (Test-Path -LiteralPath $Config.chromeExe -PathType Leaf)) { $missing += "Chrome ($($Config.chromeExe)): winget install --id Google.Chrome --exact" }
    if (-not (Test-Path -LiteralPath (Join-Path $Config.nodecgRoot 'start.js') -PathType Leaf)) { $missing += "NodeCG with this bundle ($($Config.nodecgRoot)): Install-PartyVisuals.ps1 -LightshowUrl http://<engine host>:3000, without -Autostart" }
    if (-not (Test-Path -LiteralPath $Config.helperPath -PathType Leaf)) { $missing += "Capture helper ($($Config.helperPath)): tools/browser-visuals of the matching Lightshow release, then npm.cmd ci --omit=dev" }
    if ($missing.Count) { throw ("Install these first (controller/README.md, Installation), then run this again:`n  " + ($missing -join "`n  ")) }
}
function Invoke-ControllerInstaller([string] $Installer, [string[]] $Arguments) {
    & $WindowsPowerShell -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $Installer @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Install-PartyVisualsController.ps1 $($Arguments[1]) failed (exit code $LASTEXITCODE)." }
}
function Invoke-ReleaseInstall {
    if ($ConfigPath -and $ImportLegacy) { throw 'Use either -ConfigPath or -ImportLegacy.' }
    $Root = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Root)
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    $directory = Join-Path $Root ('installer\' + $Tag)
    $hash = Get-VerifiedRelease $directory
    Write-Host "Release $Tag verified; party-visuals.exe SHA-256 $hash"
    $installer = Join-Path $directory 'Install-PartyVisualsController.ps1'
    $stage = @('-Action', 'Stage', '-Root', $Root, '-ControllerExe', (Join-Path $directory 'party-visuals.exe'), '-ExpectedSha256', $hash)
    if ($ConfigPath) {
        $stage += @('-ConfigPath', (Resolve-Path -LiteralPath $ConfigPath).ProviderPath)
    } elseif ($ImportLegacy) {
        $stage += '-ImportLegacy'
    } elseif ($MusicAssistantUrl -or $Player -or -not (Test-Path -LiteralPath (Join-Path $Root 'controller\config.json'))) {
        $url = if ($MusicAssistantUrl) { $MusicAssistantUrl } else { Read-Host 'Music Assistant address, http://host:8095' }
        $id = if ($Player) { $Player } else { Read-Host 'Music Assistant player id (the player= value of its now-playing page)' }
        $chrome = if ($ChromeExe) { $ChromeExe } else { Find-Chrome }
        $node = if ($NodeExe) { $NodeExe } else { Find-Node }
        $nodecg = if ($NodecgConfig) { $NodecgConfig } else { Join-Path $Root 'EclipseGraphics\cfg\party-visuals.json' }
        $config = New-ControllerConfig $url $id $chrome $node $nodecg
        Assert-Prerequisites $config
        $candidate = Join-Path $directory 'controller-config.json'
        [IO.File]::WriteAllText($candidate, ($config | ConvertTo-Json), (New-Object Text.UTF8Encoding($false)))
        $stage += @('-ConfigPath', $candidate)
    }
    Invoke-ControllerInstaller $installer $stage
    if ($Cutover) {
        Invoke-ControllerInstaller $installer @('-Action', 'Cutover', '-Root', $Root)
    } else {
        Write-Host "Activate it with: powershell -NoProfile -ExecutionPolicy Bypass -File `"$installer`" -Action Cutover"
    }
    $exe = Join-Path $Root ('controller\releases\' + $hash.Substring(0, 12) + '\party-visuals.exe')
    Write-Host "Use: & `"$exe`" start|stop|status --json|menu, or the tray icon. Visuals stay Off until Start."
}

if ($MyInvocation.InvocationName -eq '.') { return }
if (-not $Tag) { Write-Host $Usage; exit 2 }
Invoke-ReleaseInstall
