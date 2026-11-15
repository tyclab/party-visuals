#Requires -Version 5.1
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot '..\install\Install-PartyVisualsController.ps1')
# Run with -File, this script's functions are global and a module that loads later
# replaces them with its own. The module loads first, and each of its commands that
# changes a task throws until a check below replaces it.
Import-Module ScheduledTasks
$guarded = 'Register-ScheduledTask','Unregister-ScheduledTask','Set-ScheduledTask','Enable-ScheduledTask','Disable-ScheduledTask','Start-ScheduledTask','Stop-ScheduledTask'
foreach ($name in $guarded) {
    Set-Item -Path "function:global:$name" -Value ([scriptblock]::Create("throw 'the test reached the real $name'"))
}
$sandbox = Join-Path ([IO.Path]::GetTempPath()) ('party-controller-test-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $sandbox | Out-Null
$Root = $sandbox
$StateRoot = $sandbox
$JournalPath = Join-Path $sandbox 'migration.json'
$script:checks = 0
function Check($Name, $Ok) { if (-not $Ok) { throw "FAIL $Name" }; $script:checks++; Write-Host "ok $Name" }
try {
    $protected = Join-Path $sandbox 'protected'
    New-Item -ItemType Directory -Path $protected | Out-Null
    Protect-ControllerFolder $protected
    Protect-ControllerFolder $protected
    $acl = Get-Acl -LiteralPath $protected
    $allowed = @($acl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]) | ForEach-Object { $_.IdentityReference.Value })
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    Check 'folder protection repeats without SACL privilege and allows only current user and SYSTEM' ($acl.AreAccessRulesProtected -and $allowed.Count -eq 2 -and $allowed -contains $sid -and $allowed -contains 'S-1-5-18')
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
    $wrapped = [pscustomobject]@{ TaskName='test'; Actions=@([pscustomobject]@{Execute=(Join-Path $env:SystemRoot 'System32\conhost.exe');Arguments=('--headless "' + (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe') + '" -NoProfile -File "' + $expected + '"')}) }
    Assert-OwnedTask $wrapped $expected
    Check 'existing system conhost headless wrapper is recognized' $true
    $wrapped.Actions[0].Arguments = '--headless C:\other\powershell.exe -File "' + $expected + '"'
    $refused = $false
    try { Assert-OwnedTask $wrapped $expected } catch { $refused = $true }
    Check 'headless wrapper cannot substitute another executable' $refused
    $owned.Actions[0].Arguments = '-File C:\foreign.ps1 -Note "' + $expected + '"'
    $refused = $false
    try { Assert-OwnedTask $owned $expected } catch { $refused = $true }
    Check 'mentioning owned filename does not grant takeover' $refused
    function Get-ScheduledTask($TaskName) { return $null }
    $task = New-ControllerTaskDefinition (Join-Path $sandbox 'releases\abc\party-visuals.exe')
    $watch = @($task.Triggers | Where-Object { $_.Repetition -and $_.Repetition.Interval -eq 'PT1M' })
    Check 'the task starts at logon and asks every minute without end, ignoring the start while the controller runs' ($task.Triggers.Count -eq 2 -and $watch.Count -eq 1 -and -not $watch[0].Repetition.Duration -and "$($task.Settings.MultipleInstances)" -eq 'IgnoreNew' -and $task.Action.Arguments -eq ('--root "' + $sandbox + '" app'))
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
    $running = [pscustomobject]@{ TaskName = $ControllerTask; Actions = @([pscustomobject]@{ Execute = (Join-Path $sandbox 'releases\abc\party-visuals.exe'); Arguments = '' }) }
    function Get-ScheduledTask($TaskName) { return $running }
    function Disable-ScheduledTask($TaskName) { $script:events += 'disabled' }
    $realQuit = ${function:Send-ControllerQuit}
    function Send-ControllerQuit($Exe) { $script:events += 'quit' }
    function Wait-ControllerReady($Exe) { $script:events += 'ready' }
    Save-ControllerJson $path @{ version = 1; manualOff = $false; requested = $true; mode = 'follow-party' }
    $script:events = @()
    Cutover-Controller $journal
    $loaded = Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
    Check 'cutover stops the running controller before it takes the lock, and visuals begin Off' (($script:events -join ',') -eq 'check,disabled,quit,registered,start,ready' -and $loaded.manualOff -and -not $loaded.requested -and $journal.phase -eq 'active')
    function Register-ControllerTask($Exe) { throw 'registration refused' }
    function Enable-ScheduledTask($TaskName) { $script:events += 'enabled' }
    $script:events = @()
    $failed = $false
    try { Cutover-Controller $journal } catch { $failed = $true }
    $held = $false
    try { (Enter-ControllerOwnership).Dispose() } catch { $held = $true }
    Check 'a cutover that fails before it registers leaves the task disabled and lets go of the lock' ($failed -and ($script:events -join ',') -eq 'check,disabled,quit' -and -not $held)
    $ControllerStopSeconds = 1
    $owner = Enter-ControllerOwnership
    $script:events = @()
    $message = ''
    try { Cutover-Controller $journal } catch { $message = $_.Exception.Message } finally { $owner.Dispose() }
    Check 'a controller that keeps its lock stops the cutover before anything is registered' ($message -like 'The running controller has not stopped*' -and ($script:events -join ',') -eq 'check,disabled,quit')
    $skipped = $true
    try { & $realQuit (Join-Path $sandbox 'releases\gone\party-visuals.exe') } catch { $skipped = $false }
    Check 'a quit to a controller whose executable is gone is skipped, not an error' $skipped
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
} finally {
    Remove-Item -LiteralPath $sandbox -Recurse -Force
    # Called with &, the throwing commands would stay in the caller's session.
    foreach ($name in $guarded) { Remove-Item -Path "function:global:$name" -ErrorAction SilentlyContinue }
    Import-Module ScheduledTasks -Force
}
