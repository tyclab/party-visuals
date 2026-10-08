#Requires -Version 5.1
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$script:passed = 0
function Check([string] $Name, [bool] $Ok) {
    if (-not $Ok) { throw "FAIL $Name" }
    $script:passed++
    Write-Host "ok $Name"
}
. (Join-Path $PSScriptRoot '..\install\Start-PartyVisualsOBS.ps1') -Profile 'Untitled'
$script:requests = @()
$script:reply = 200
function Invoke-WebRequest {
    param([switch] $UseBasicParsing, $Uri, $TimeoutSec)
    $script:requests += $Uri
    if ($script:reply -eq 0) { throw 'mock offline' }
    return @{ StatusCode = $script:reply }
}
Check 'both sources ready' (Test-VisualsReady)
Check 'both sources checked on loopback' ($script:requests.Count -eq 2 -and $script:requests[0] -eq 'http://127.0.0.1:9090/bundles/party-visuals/graphics/wash.html' -and $script:requests[1] -eq 'http://127.0.0.1:9090/bundles/party-visuals/graphics/bar.html')
$script:reply = 503
Check 'server error defers launch' (-not (Test-VisualsReady))
$script:reply = 0
Check 'offline NodeCG defers launch' (-not (Test-VisualsReady))
$script:reply = 200
$script:started = $null
$script:existing = $false
$script:currentSession = (Get-Process -Id $PID).SessionId
function Test-Path { param($LiteralPath, $PathType); return $true }
function Get-Process {
    param($Id, $Name, $ErrorAction)
    if ($Id -or $script:existing) { return @{ SessionId = $script:currentSession } }
}
function Start-Process {
    param($FilePath, $WorkingDirectory, $ArgumentList, [switch] $PassThru)
    $script:started = @{ File = $FilePath; Dir = $WorkingDirectory; Args = $ArgumentList }
    $p = New-Object PSObject -Property @{ ExitCode = 17 }
    $p | Add-Member -MemberType ScriptMethod -Name WaitForExit -Value { }
    return $p
}
$ObsExe = 'C:\Program Files\obs-studio\bin\64bit\obs64.exe'
Check 'OBS failure exit code propagates for recovery' ((Start-VisualsOBS) -eq 17)
Check 'launch retains collection profile and scene without output flags' ($script:started.Args -eq '--collection "Party Visuals" --profile "Untitled" --scene "Party Visuals" --minimize-to-tray')
Check 'launch uses OBS binary working directory' ($script:started.Dir -eq 'C:\Program Files\obs-studio\bin\64bit')
$script:existing = $true
$script:started = $null
Check 'existing desktop OBS avoids a second process' ((Start-VisualsOBS) -eq 0 -and $null -eq $script:started)
Write-Host "$script:passed passed, 0 failed"
