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
$script:displays = @()
function Get-DesktopDisplays { $script:displays }
Check 'no active TV defers launch' ($null -eq (Get-VisualsDisplay))
$lg = [pscustomobject]@{ Name = '\\.\DISPLAY2'; HardwareId = 'MONITOR\GSM7754\instance'; Active = $true; X = 0; Y = 0; Width = 3840; Height = 1600 }
$tv = [pscustomobject]@{ Name = '\\.\DISPLAY3'; HardwareId = 'MONITOR\SAM7140\instance'; Active = $true; X = -1920; Y = -2160; Width = 3840; Height = 2160 }
$script:displays = @($lg)
Check 'LG is never a fallback' ($null -eq (Get-VisualsDisplay))
$script:displays = @($lg, $tv)
$chosen = Get-VisualsDisplay
Check 'TV uses hardware ID and current negative desktop bounds' ($chosen.Name -eq '\\.\DISPLAY3' -and $chosen.X -eq -1920 -and $chosen.Y -eq -2160 -and $chosen.Height -eq 2160)
$tv.Active = $false
Check 'remembered inactive TV is not usable' ($null -eq (Get-VisualsDisplay))
$tv.Active = $true
$script:displays = @($tv, $tv)
Throws 'ambiguous matching TVs fail safely' { Get-VisualsDisplay }
$script:displays = @($tv)
$BrowserProfile = 'C:\Users\Test User\AppData\Local\PartyVisuals\browser-profile'
foreach ($value in @('plain', 'C:\trailing space\', 'a"quoted"b', 'http://example.test/#/?a=1&b=2', '')) {
    $roundtrip = [PartyBrowserNative]::Arguments('program.exe ' + (ConvertTo-NativeArgument $value))
    Check ('native argument round trip: ' + $value) ($roundtrip.Count -eq 2 -and $roundtrip[1] -ceq $value)
}
Throws 'arguments reject line breaks' { ConvertTo-NativeArgument "a`nb" }
$browserArgs = @(Get-BrowserArguments $tv)
Check 'Chrome debugging is ephemeral and explicitly loopback' ($browserArgs -contains '--remote-debugging-port=0' -and $browserArgs -contains '--remote-debugging-address=127.0.0.1')
Check 'Chrome retains dedicated profile and normal auth storage' ($browserArgs -contains ('--user-data-dir=' + $BrowserProfile) -and -not ($browserArgs -match 'incognito|guest|disable-web-security'))
Check 'Chrome receives app fullscreen and exact TV geometry' ($browserArgs -contains '--start-fullscreen' -and $browserArgs -contains ('--app=' + $VisualizerUrl) -and $browserArgs -contains '--window-position=-1920,-2160' -and $browserArgs -contains '--window-size=3840,2160')
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
$script:events = @()
$script:sleeps = 0
$script:helperStops = 0
function Initialize-BrowserNative { }
function Test-Path { param($LiteralPath, $PathType); return $true }
function Start-Process { throw 'Tests must never launch a real application.' }
function Set-BrowserDisplay { param($ProcessId, $Display); $script:events += $(if ($null -eq $Display) { 'hide' } else { 'place' }); return $true }
function Start-BrowserHelper { $script:events += 'helper-start'; return [pscustomobject]@{ HasExited = $false } }
function Stop-BrowserHelper { param($Process); if ($null -ne $Process) { $script:events += 'helper-stop'; $script:helperStops++ } }
function Start-Sleep {
    param($Seconds)
    $script:sleeps++
    if ($script:sleeps -eq 1) { $script:displays = @() }
    if ($script:sleeps -ge 2) { throw 'test-loop-complete' }
}
$previousProfile = $BrowserProfile
$BrowserProfile = Join-Path $env:LOCALAPPDATA 'Google\Chrome\User Data\Default'
Throws 'normal Chrome profile cannot be used by launcher' { Start-VisualsBrowser }
$BrowserProfile = $previousProfile
$script:displays = @($tv)
try { Start-VisualsBrowser | Out-Null } catch { if ($_.Exception.Message -ne 'test-loop-complete') { throw } }
Check 'helper starts only after exact TV placement' (($script:events -join ',').StartsWith('place,helper-start'))
Check 'TV disconnect hides owned window and stops helper' (($script:events -join ',') -eq 'place,helper-start,hide,helper-stop')
$script:events = @()
$script:sleeps = 0
$script:processes = @()
$script:displays = @()
try { Start-VisualsBrowser | Out-Null } catch { if ($_.Exception.Message -ne 'test-loop-complete') { throw } }
Check 'absent TV starts neither browser nor helper' ($script:events.Count -eq 0)
Write-Host "$script:passed passed, 0 failed"
