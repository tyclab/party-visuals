#Requires -Version 5.1
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$script:passed = 0
function Check([string] $Name, [bool] $Ok) {
    if (-not $Ok) { throw "FAIL $Name" }
    $script:passed++
    Write-Host "ok $Name"
}
function Throws([string] $Name, [scriptblock] $Action) {
    $failed = $false
    try { & $Action | Out-Null } catch { $failed = $true }
    Check $Name $failed
}
. (Join-Path $PSScriptRoot '..\install\Start-PartyVisualsBrowser.ps1')
Initialize-BrowserNative
$BrowserProfile = 'C:\Users\Test User\AppData\Local\PartyVisuals\browser-profile'
foreach ($value in @('plain', 'C:\trailing space\', 'a"quoted"b', 'http://example.test/#/?a=1&b=2', '')) {
    $roundtrip = [PartyBrowserNative]::Arguments('program.exe ' + (ConvertTo-NativeArgument $value))
    Check ('native argument round trip: ' + $value) ($roundtrip.Count -eq 2 -and $roundtrip[1] -ceq $value)
}
Throws 'arguments reject line breaks' { ConvertTo-NativeArgument "a`nb" }
$browserArgs = @(Get-BrowserArguments)
Check 'Chrome debugging is ephemeral and explicitly loopback' ($browserArgs -contains '--remote-debugging-port=0' -and $browserArgs -contains '--remote-debugging-address=127.0.0.1')
Check 'Chrome retains dedicated profile and normal auth storage' ($browserArgs -contains ('--user-data-dir=' + $BrowserProfile) -and -not ($browserArgs -match 'incognito|guest|disable-web-security'))
Check 'Chrome opens the visualizer app fullscreen' ($browserArgs -contains '--start-fullscreen' -and $browserArgs -contains ('--app=' + $VisualizerUrl))
Check 'no window geometry, so Chrome keeps the screen the operator chose' (-not ($browserArgs -match '^--window-(position|size)='))
$helperArgs = @(Get-HelperArguments)
Check 'display helper has parent lifecycle and no curtain by default' ($helperArgs -contains '--parent-pid' -and $helperArgs -contains '--controls-url' -and $helperArgs -notcontains '--curtain')
$Curtain = $true
$helperArgs = @(Get-HelperArguments)
Check 'optional curtain helper receives logical geometry and capped frame rate' (($helperArgs -join '|') -match '\|--curtain\|--nodecg-config\|' -and ($helperArgs -join '|').EndsWith('|--width|68|--height|42|--fps|10|--fit|cover'))
$Curtain = $false
$script:processes = @()
function Get-CimInstance { param($ClassName, $Filter); $script:processes }
function Get-Process { param($Id, $ErrorAction); [pscustomobject]@{ Id = $Id; SessionId = 77; HasExited = $false } }
Check 'normal Chrome has no dedicated profile owner' ($null -eq (Get-ProfileBrowser))
$command = 'chrome.exe ' + (($browserArgs | ForEach-Object { ConvertTo-NativeArgument $_ }) -join ' ')
$owner = [pscustomobject]@{ SessionId = 77; ProcessId = 123; CommandLine = $command }
$script:processes = @([pscustomobject]@{ SessionId = 77; ProcessId = 456; CommandLine = 'chrome.exe' }, $owner)
Check 'only matching dedicated Chrome is adopted' ((Get-ProfileBrowser).Id -eq 123)
$owner.CommandLine = $command.Replace(' "--start-fullscreen"', '')
Check 'a dedicated window started without fullscreen is still adopted' ((Get-ProfileBrowser).Id -eq 123)
$owner.CommandLine = $command + ' --type=renderer'
Check 'renderer subprocess cannot own profile' ($null -eq (Get-ProfileBrowser))
$owner.CommandLine = $command.Replace('--remote-debugging-port=0', '--remote-debugging-port=9222')
Throws 'profile with incompatible startup flags is not taken over' { Get-ProfileBrowser }
$owner.CommandLine = $command
$script:processes = @($owner, $owner)
Throws 'duplicate profile owners fail safely' { Get-ProfileBrowser }
$script:processes = @($owner)
$script:killed = $false
$script:disposed = $false
$script:waitReady = $true
$stopping = New-Object PSObject -Property @{ HasExited = $false; StandardInput = (New-Object IO.StringWriter) }
$stopping | Add-Member ScriptMethod WaitForExit { param($Milliseconds); return ($script:waitReady -or $script:killed) }
$stopping | Add-Member ScriptMethod Kill { $script:killed = $true }
$stopping | Add-Member ScriptMethod Dispose { $script:disposed = $true }
Stop-BrowserHelper $stopping
Check 'helper receives graceful stdin stop before disposal' ($stopping.StandardInput.ToString().Trim() -eq 'stop' -and $script:disposed -and -not $script:killed)
$script:waitReady = $false
Stop-BrowserHelper $stopping
Check 'unresponsive owned helper is terminated before retry' $script:killed
function Initialize-BrowserNative { }
function New-Item { param($ItemType, $Path, [switch] $Force) }
function Start-BrowserHelper { $script:events += 'helper-start'; return [pscustomobject]@{ HasExited = $false } }
function Stop-BrowserHelper { param($Process); if ($null -ne $Process) { $script:events += 'helper-stop' } }
function Start-Process { param($FilePath, $WorkingDirectory, $ArgumentList, [switch] $PassThru); $script:events += 'browser-start'; $script:launchArgs = $ArgumentList; return $script:browser }
function Start-Sleep { param($Seconds); $script:browser.HasExited = $true }
function Get-ProfileBrowser { $null }
function Test-Path { param($LiteralPath, $PathType); return $true }
$previousProfile = $BrowserProfile
$BrowserProfile = Join-Path $env:LOCALAPPDATA 'Google\Chrome\User Data\Default'
Throws 'normal Chrome profile cannot be used by launcher' { Start-VisualsBrowser }
$BrowserProfile = $previousProfile
function Test-Path { param($LiteralPath, $PathType); return ($PathType -eq 'Leaf') }
$script:browser = [pscustomobject]@{ HasExited = $false }
$script:events = @()
$code = Start-VisualsBrowser
Check 'opens the browser once, runs the helper, and stops it when the window closes' ($code -eq 0 -and ($script:events -join ',') -eq 'browser-start,helper-start,helper-stop')
Check 'launch carries no window geometry' ($script:launchArgs -notmatch '--window-(position|size)=')
$script:browser = [pscustomobject]@{ HasExited = $false }
function Get-ProfileBrowser { $script:browser }
$script:events = @()
[void](Start-VisualsBrowser)
Check 'an already open dedicated browser is adopted, not relaunched' (($script:events -join ',') -eq 'helper-start,helper-stop')
Write-Host "$script:passed passed, 0 failed"
