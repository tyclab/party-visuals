#Requires -Version 5.1
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '..\install\Install-PartyVisualsController.ps1')
$sandbox = Join-Path ([IO.Path]::GetTempPath()) ('party-controller-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $sandbox | Out-Null
$Root = $sandbox
$StateRoot = $sandbox
$JournalPath = Join-Path $sandbox 'migration.json'
$script:checks = 0
function Check($Name, $Ok) { if (-not $Ok) { throw "FAIL $Name" }; $script:checks++; Write-Host "ok $Name" }
try {
    $path = Join-Path $sandbox 'intent.json'
    Save-ControllerJson $path @{ version = 1; manualOff = $true; requested = $false; mode = 'manual' }
    Save-ControllerJson $path @{ version = 1; manualOff = $true; requested = $false; mode = 'follow-party' }
    $loaded = Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
    Check 'atomic intent replacement retains Off' ($loaded.manualOff -and -not $loaded.requested -and $loaded.mode -eq 'follow-party')
    $guard = Enter-ControllerOwnership
    $refused = $false
    try { $other = Enter-ControllerOwnership; $other.Dispose() } catch { $refused = $true }
    $guard.Dispose()
    Check 'installer refuses a held controller lock' $refused
    $foreign = [pscustomobject]@{ TaskName = 'test'; Actions = @([pscustomobject]@{Execute='C:\other.exe';Arguments=''}) }
    $refused = $false
    try { Assert-OwnedTask $foreign 'C:\PartyVisuals\startup\Start-PartyVisualsBrowser.ps1' } catch { $refused = $true }
    Check 'foreign task cannot be taken over' $refused
    $expected = Join-Path $sandbox 'startup\Start-PartyVisualsBrowser.ps1'
    $owned = [pscustomobject]@{ TaskName='test'; Actions=@([pscustomobject]@{Execute='powershell.exe';Arguments=('-NoProfile -File "' + $expected + '" -BrowserProfile "C:\dedicated profile"')}) }
    Assert-OwnedTask $owned $expected
    Check 'owned task matches exact File argument and preserves profile Stop argument' ((@(Get-StopArguments $owned) -join '|') -eq '-BrowserProfile|C:\dedicated profile')
    $owned.Actions[0].Arguments = '-File C:\foreign.ps1 -Note "' + $expected + '"'
    $refused = $false
    try { Assert-OwnedTask $owned $expected } catch { $refused = $true }
    Check 'mentioning owned filename does not grant takeover' $refused
    $script:events = @()
    function Test-ControllerBinary($Exe) { $script:events += 'check' }
    function Register-ControllerTask($Exe) { $script:events += 'register'; $guard = Enter-ControllerOwnership; $guard.Dispose() }
    function Start-ScheduledTask($TaskName) { $script:events += 'start'; $guard = Enter-ControllerOwnership; $guard.Dispose() }
    $candidate = Join-Path $sandbox 'candidate.json'; [IO.File]::WriteAllText($candidate,'{}')
    function Wait-ControllerReady($Exe) { $script:events += 'ready' }
    $journal = [pscustomobject]@{ version=1;phase='staged';exe='fake.exe';config=$candidate;tasks=@() }
    $failed = $false
    try { Cutover-Controller $journal } catch { $failed = $true }
    Check 'cutover owns the controller lock before registration' ($failed -and ($script:events -join ',') -eq 'check,register')
    Check 'interrupted cutover never starts a second owner' ($journal.phase -eq 'legacy-stopped')
    function Register-ControllerTask($Exe) { $script:events += 'registered' }
    Cutover-Controller $journal
    Check 'new task starts only after ownership guard is released' ($journal.phase -eq 'active' -and $script:events[-2] -eq 'start' -and $script:events[-1] -eq 'ready')
    function Wait-ControllerReady($Exe) { throw 'unreachable' }
    $failed = $false
    try { Cutover-Controller $journal } catch { $failed = $true }
    Check 'unreachable owner is never reported active' ($failed -and $journal.phase -eq 'registered')
    $script:events = @()
    function Get-ScheduledTask($TaskName) { return $null }
    function Register-ScheduledTask($TaskName,$Xml,[switch]$Force) { $script:events += 'restore' }
    function Disable-ScheduledTask($TaskName) { $script:events += 'disabled' }
    $xml = Join-Path $sandbox 'prior.xml'; [IO.File]::WriteAllText($xml,'<Task/>')
    $journal.tasks=@([pscustomobject]@{name='owned test task';xml=$xml;enabled=$true})
    Rollback-Controller $journal
    $loaded=Get-Content -LiteralPath $path -Raw|ConvertFrom-Json
    Check 'rollback restores definitions disabled and preserves current Off' (($script:events -join ',') -eq 'restore,disabled' -and $loaded.manualOff -and -not $loaded.requested -and $journal.phase -eq 'rolled-back-off')
    Write-Host "Passed $script:checks controller installer checks. No real task was registered or stopped."
} finally { Remove-Item -LiteralPath $sandbox -Recurse -Force }
