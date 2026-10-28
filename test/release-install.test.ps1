#Requires -Version 5.1
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '..\install\Install-PartyVisualsRelease.ps1')
$sandbox = Join-Path ([IO.Path]::GetTempPath()) ('party-release-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $sandbox | Out-Null
$script:checks = 0
function Check($Name, $Ok) { if (-not $Ok) { throw "FAIL $Name" }; $script:checks++; Write-Host "ok $Name" }
function Write-Sums([string[]] $Names) {
    $lines = foreach ($name in $Names) { '{0}  {1}' -f (Get-FileHash -LiteralPath (Join-Path $published $name) -Algorithm SHA256).Hash.ToLowerInvariant(), $name }
    [IO.File]::WriteAllText((Join-Path $published 'SHA256SUMS'), (($lines -join "`n") + "`n"))
}
try {
    $published = Join-Path $sandbox 'published'
    New-Item -ItemType Directory -Path $published | Out-Null
    foreach ($name in $Assets) { [IO.File]::WriteAllText((Join-Path $published $name), "fixture $name") }
    Write-Sums $Assets
    function Save-ReleaseAsset([string] $Name, [string] $Path) { Copy-Item -LiteralPath (Join-Path $published $Name) -Destination $Path }
    $Root = Join-Path $sandbox 'root'
    $Tag = 'v9.9.9'
    $target = Join-Path $Root 'installer\v9.9.9'
    $exeHash = (Get-FileHash -LiteralPath (Join-Path $published 'party-visuals.exe') -Algorithm SHA256).Hash.ToLowerInvariant()
    $hash = Get-VerifiedRelease $target
    Check 'verified release returns the executable checksum and keeps every asset' ($hash -eq $exeHash -and @($Assets | Where-Object { -not (Test-Path -LiteralPath (Join-Path $target $_)) }).Count -eq 0)
    [IO.File]::WriteAllText((Join-Path $published 'Run-PartyVisualsSession.ps1'), 'substituted')
    $message = ''
    try { Get-VerifiedRelease $target | Out-Null } catch { $message = $_.Exception.Message }
    Check 'a substituted asset is refused and the download removed' ($message -like 'Checksum mismatch for Run-PartyVisualsSession.ps1*' -and -not (Test-Path -LiteralPath $target))
    Write-Sums @($Assets | Where-Object { $_ -ne 'Start-PartyVisualsNodecg.ps1' })
    $message = ''
    try { Get-VerifiedRelease $target | Out-Null } catch { $message = $_.Exception.Message }
    Check 'an asset without a published checksum is refused' ($message -like '*no checksum for Start-PartyVisualsNodecg.ps1*')
    Write-Sums $Assets
    $ExpectedSha256 = 'a' * 64
    $message = ''
    try { Get-VerifiedRelease $target | Out-Null } catch { $message = $_.Exception.Message }
    Check 'a reviewed hash pin overrides the release checksums' ($message -like '*does not match -ExpectedSha256*' -and -not (Test-Path -LiteralPath $target))
    $ExpectedSha256 = $exeHash.ToUpperInvariant()
    Check 'a matching reviewed hash pin is accepted' ((Get-VerifiedRelease $target) -eq $exeHash)
    $ExpectedSha256 = ''
    $config = New-ControllerConfig 'http://music.example:8095/' ' 02:00:00:00:00:01 ' 'C:\chrome.exe' 'C:\node.exe' 'C:\cfg.json'
    Check 'visualizer URL selects the escaped player without a query' ($config.visualizerUrl -eq 'http://music.example:8095/#/now-playing?player=02%3A00%3A00%3A00%3A00%3A01&frameless=1')
    Check 'configuration keeps NodeCG on loopback and every output off' ($config.controlsUrl -eq 'http://127.0.0.1:9090/bundles/party-visuals/api/state' -and -not $config.curtain -and -not $config.room -and $config.panels -eq '' -and $config.nodecgRoot -eq (Join-Path $Root 'EclipseGraphics'))
    $refused = 0
    foreach ($bad in @('http://user:secret@music.example:8095', 'http://music.example:8095/?token=1', 'ftp://music.example', 'music.example:8095')) {
        try { New-ControllerConfig $bad 'player' 'C:\chrome.exe' 'C:\node.exe' 'C:\cfg.json' | Out-Null } catch { $refused++ }
    }
    try { New-ControllerConfig 'http://music.example:8095' ' ' 'C:\chrome.exe' 'C:\node.exe' 'C:\cfg.json' | Out-Null } catch { $refused++ }
    Check 'credentials, queries, other schemes and an empty player are refused' ($refused -eq 5)
    $message = ''
    try { Assert-Prerequisites $config } catch { $message = $_.Exception.Message }
    Check 'missing prerequisites are all named before staging' ($message -like '*Node.js*' -and $message -like '*Chrome*' -and $message -like '*NodeCG*' -and $message -like '*Capture helper*')
    $script:calls = @()
    function Invoke-ControllerInstaller([string] $Installer, [string[]] $Arguments) { $script:calls += ,($Arguments -join ' ') }
    $ConfigPath = Join-Path $sandbox 'reviewed.json'
    [IO.File]::WriteAllText($ConfigPath, '{}')
    $reviewed = (Resolve-Path -LiteralPath $ConfigPath).ProviderPath
    Invoke-ReleaseInstall
    Check 'stage receives the verified hash and the reviewed configuration, and nothing is activated' ($script:calls.Count -eq 1 -and $script:calls[0] -eq "-Action Stage -Root $Root -ControllerExe $target\party-visuals.exe -ExpectedSha256 $exeHash -ConfigPath $reviewed")
    $script:calls = @()
    $Cutover = [switch]$true
    Invoke-ReleaseInstall
    Check 'explicit -Cutover activates only after Stage' ($script:calls.Count -eq 2 -and $script:calls[0] -like '-Action Stage *' -and $script:calls[1] -eq "-Action Cutover -Root $Root")
    $script:calls = @()
    function Invoke-ControllerInstaller([string] $Installer, [string[]] $Arguments) { $script:calls += ,($Arguments -join ' '); throw 'stage failed' }
    $failed = $false
    try { Invoke-ReleaseInstall } catch { $failed = $true }
    Check 'a failed Stage never reaches Cutover' ($failed -and $script:calls.Count -eq 1)
    Write-Host "Passed $script:checks release installer checks. Nothing was downloaded, staged or registered."
} finally { Remove-Item -LiteralPath $sandbox -Recurse -Force }
