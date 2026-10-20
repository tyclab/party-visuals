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
$launcherFile = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\install\Start-PartyVisualsBrowser.ps1'))
. $launcherFile
# A running visuals task polls the real stop file and owns the real task name; the tests must not reach either.
$StopFile = Join-Path ([IO.Path]::GetTempPath()) ('party-visuals-stop-' + [guid]::NewGuid().ToString('N'))
$TaskName = 'PartyVisuals Browser Test ' + [guid]::NewGuid().ToString('N')
Initialize-BrowserNative
$BrowserProfile = 'C:\Users\Test User\AppData\Local\PartyVisuals\browser-profile'
foreach ($value in @('plain', 'C:\trailing space\', 'a"quoted"b', 'http://example.test/#/?a=1&b=2', '')) {
    $roundtrip = [PartyBrowserNative]::Arguments('program.exe ' + (ConvertTo-NativeArgument $value))
    Check ('native argument round trip: ' + $value) ($roundtrip.Count -eq 2 -and $roundtrip[1] -ceq $value)
}
Throws 'arguments reject line breaks' { ConvertTo-NativeArgument "a`nb" }
$browserArgs = @(Get-BrowserArguments)
Check 'Chrome debugging is ephemeral and explicitly loopback' ($browserArgs -contains '--remote-debugging-port=0' -and $browserArgs -contains '--remote-debugging-address=127.0.0.1')
Check 'dedicated visualizer keeps rendering and capture timers when unfocused' ($browserArgs -contains '--disable-backgrounding-occluded-windows' -and $browserArgs -contains '--disable-renderer-backgrounding' -and $browserArgs -contains '--disable-background-timer-throttling')
Check 'Chrome retains dedicated profile and normal auth storage' ($browserArgs -contains ('--user-data-dir=' + $BrowserProfile) -and -not ($browserArgs -match 'incognito|guest|disable-web-security'))
Check 'Chrome opens the visualizer app fullscreen' ($browserArgs -contains '--start-fullscreen' -and $browserArgs -contains ('--app=' + $VisualizerUrl))
Check 'no window geometry, so Chrome keeps the screen the operator chose' (-not ($browserArgs -match '^--window-(position|size)='))
$helperArgs = @(Get-HelperArguments)
Check 'display helper has parent lifecycle and no curtain by default' ($helperArgs -contains '--parent-pid' -and $helperArgs -contains '--controls-url' -and $helperArgs -notcontains '--curtain')
Check 'display-only helper does not collect pixel telemetry' ($helperArgs -notcontains '--telemetry-file')
$Curtain = $true
$helperArgs = @(Get-HelperArguments)
Check 'curtain geometry leaves the default refresh rate to the helper' (($helperArgs -join '|') -match '\|--curtain\|--nodecg-config\|' -and ($helperArgs -join '|').EndsWith('|--width|68|--height|42|--fit|cover') -and $helperArgs -notcontains '--fps')
Check 'curtain diagnostics use the bounded local telemetry file' ($helperArgs -contains '--telemetry-file' -and $helperArgs -contains $TelemetryFile)
$Curtain = $false
$Room = $true
$helperArgs = @(Get-HelperArguments)
Check 'optional room picture helper gets the output configuration once' (($helperArgs -join '|') -match '\|--room\|--nodecg-config\|' -and $helperArgs -notcontains '--curtain')
$Curtain = $true
$helperArgs = @(Get-HelperArguments)
Check 'curtain and room together share one configuration path' ((@($helperArgs | Where-Object { $_ -eq '--nodecg-config' })).Count -eq 1 -and $helperArgs -contains '--room' -and $helperArgs -contains '--curtain')
Check 'both outputs share one telemetry file argument' ((@($helperArgs | Where-Object { $_ -eq '--telemetry-file' })).Count -eq 1)
$Fps = 60
$helperArgs = @(Get-HelperArguments)
Check 'a requested refresh rate is passed once for both outputs' ((@($helperArgs | Where-Object { $_ -eq '--fps' })).Count -eq 1 -and ($helperArgs -join '|').EndsWith('|--fps|60'))
$Curtain = $false
$helperArgs = @(Get-HelperArguments)
Check 'room-only output accepts a requested refresh rate' (($helperArgs -join '|').EndsWith('|--fps|60'))
$Curtain = $false
$Room = $false
$helperArgs = @(Get-HelperArguments)
Check 'display-only mode does not request pixel captures' ($helperArgs -notcontains '--fps')
$Fps = 0

Check 'the session record matches the Windows WTSINFOW layout' ([Runtime.InteropServices.Marshal]::SizeOf([type]'PartyBrowserNative+SessionInfo') -eq 216)
$here = Get-DisplaySession
Check 'this session reports its connection and sign-in times' ($here.Active -is [bool] -and $here.Station -is [string] -and $here.LogonAt -gt [datetime]'2000-01-01' -and $here.LogonAt -le [datetime]::UtcNow -and $here.ConnectedAt -le [datetime]::UtcNow)
Check 'an unknown session cannot be queried' ($null -eq [PartyBrowserNative]::Session(2147483000))

$signIn = [pscustomobject]@{ LogonAt = [datetime]::UtcNow.AddHours(-1) }
Check 'no stop file, no stop' (-not (Test-StopRequested $signIn))
[IO.File]::WriteAllText($StopFile, 'stop')
Check 'a stop made during this sign-in holds' (Test-StopRequested $signIn)
[IO.File]::SetLastWriteTimeUtc($StopFile, [datetime]::UtcNow.AddHours(-2))
Check 'a stop from an earlier sign-in is dropped and removed' (-not (Test-StopRequested $signIn) -and -not [IO.File]::Exists($StopFile))
[IO.File]::WriteAllText($StopFile, 'stop')
[IO.File]::SetLastWriteTimeUtc($StopFile, [datetime]::UtcNow.AddHours(-2))
Check 'a stop holds when the sign-in time is unknown' (Test-StopRequested ([pscustomobject]@{ LogonAt = [datetime]::MinValue }))
[IO.File]::Delete($StopFile)

Check 'the first reopen waits 5 seconds' ((Get-RelaunchDelay 0 ([timespan]::FromSeconds(2))) -eq 5)
Check 'a browser that keeps closing at once waits twice as long each time' ((Get-RelaunchDelay 5 ([timespan]::FromSeconds(2))) -eq 10 -and (Get-RelaunchDelay 40 ([timespan]::FromSeconds(59))) -eq 80)
Check 'the pause between reopens is capped at 5 minutes' ((Get-RelaunchDelay 160 ([timespan]::FromSeconds(2))) -eq 300 -and (Get-RelaunchDelay 300 ([timespan]::FromSeconds(2))) -eq 300)
Check 'a browser that stayed up for a minute reopens after 5 seconds again' ((Get-RelaunchDelay 300 ([timespan]::FromSeconds(60))) -eq 5)

$script:events = @()
$script:closeExits = $true
function New-FakeBrowser([datetime] $Started = [datetime]::Now) {
    $fake = [pscustomobject]@{ HasExited = $false; StartTime = $Started; Handle = [IntPtr]::Zero }
    $fake | Add-Member ScriptMethod CloseMainWindow { $script:events += 'browser-close'; if ($script:closeExits) { $this.HasExited = $true }; return $true }
    $fake | Add-Member ScriptMethod WaitForExit { param($Milliseconds); return $this.HasExited }
    $fake | Add-Member ScriptMethod Kill { $script:events += 'browser-kill'; $this.HasExited = $true }
    $fake
}
Close-VisualsBrowser (New-FakeBrowser)
Check 'the browser is closed through its window, not killed' (($script:events -join ',') -eq 'browser-close')
$script:events = @()
$script:closeExits = $false
Close-VisualsBrowser (New-FakeBrowser)
Check 'a browser that ignores the close is ended' (($script:events -join ',') -eq 'browser-close,browser-kill')
$script:events = @()
$gone = New-FakeBrowser
$gone.HasExited = $true
Close-VisualsBrowser $gone
Close-VisualsBrowser $null
Check 'an exited or missing browser is left alone' ($script:events.Count -eq 0)
$script:closeExits = $true

Check 'the launcher knows its own file for the task' ($LauncherPath -eq $launcherFile)
$script:registered = $null
function Register-ScheduledTask { param($TaskName, $Xml, [switch] $Force); $script:registered = @{ Name = $TaskName; Xml = $Xml; Force = [bool]$Force } }
$bound = @{ Register = [switch]$true; Stop = [switch]$false; TaskName = 'other'; Verbose = [switch]$true; Room = [switch]$true; Curtain = [switch]$true; Fps = 60; VisualizerUrl = 'http://h.test/#/?a=1&b=2' }
Check 'registration succeeds' ((Register-VisualsTask $bound) -eq 0)
$task = ([xml]$script:registered.Xml).Task
$user = [Security.Principal.WindowsIdentity]::GetCurrent().Name
Check 'registration replaces the task of the configured name' ($script:registered.Name -eq $TaskName -and $script:registered.Force)
Check 'the task starts at logon and when the session connects or unlocks' ($task.Triggers.LogonTrigger.UserId -eq $user -and (@($task.Triggers.SessionStateChangeTrigger | ForEach-Object { $_.StateChange }) -join ',') -eq 'ConsoleConnect,RemoteConnect,SessionUnlock' -and @($task.Triggers.SessionStateChangeTrigger | Where-Object { $_.UserId -ne $user }).Count -eq 0)
Check 'the task ignores duplicates, has no time limit and retries failures every minute' ($task.Settings.MultipleInstancesPolicy -eq 'IgnoreNew' -and $task.Settings.ExecutionTimeLimit -eq 'PT0S' -and $task.Settings.StartWhenAvailable -eq 'true' -and $task.Settings.RestartOnFailure['Count'].InnerText -eq '999' -and $task.Settings.RestartOnFailure['Interval'].InnerText -eq 'PT1M')
Check 'the task runs at normal priority, not the Task Scheduler default below normal' ($task.Settings.Priority -eq '5')
Check 'the task runs on this user''s interactive desktop with limited rights' ($task.Principals.Principal.UserId -eq $user -and $task.Principals.Principal.LogonType -eq 'InteractiveToken' -and $task.Principals.Principal.RunLevel -eq 'LeastPrivilege')
Check 'the task runs this launcher hidden in Windows PowerShell' ($task.Actions.Exec.Command -eq "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe" -and $task.Actions.Exec.WorkingDirectory -eq (Split-Path -Parent $launcherFile))
Check 'the task keeps the launch options but not the management switches' ($task.Actions.Exec.Arguments -ceq ('-NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $launcherFile + '" -Curtain -Fps "60" -Room -VisualizerUrl "http://h.test/#/?a=1&b=2"'))
$scheduler = New-Object -ComObject Schedule.Service
$scheduler.Connect()
$validated = $true
try { [void]$scheduler.GetFolder('\').RegisterTask($TaskName, $script:registered.Xml, 1, $null, $null, 3) } catch { $validated = $false; Write-Host $_.Exception.Message }
Check 'Task Scheduler accepts the task definition (validation only)' $validated
Check 'the tests registered no real task' (-not (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue))

$script:processes = @()
function Get-CimInstance { param($ClassName, $Filter); $script:processes }
function Get-Process { param($Id, $ErrorAction); [pscustomobject]@{ Id = $Id; SessionId = 2147483000; HasExited = $false } }
$unknown = Get-DisplaySession
Check 'an unknown session reads as connected, with no connection or sign-in time' ($unknown.Active -and $unknown.ConnectedAt -eq [datetime]::MinValue -and $unknown.LogonAt -eq [datetime]::MinValue)
Check 'normal Chrome has no dedicated profile owner' ($null -eq (Get-ProfileBrowser))
$command = 'chrome.exe ' + (($browserArgs | ForEach-Object { ConvertTo-NativeArgument $_ }) -join ' ')
$owner = [pscustomobject]@{ SessionId = 2147483000; ProcessId = 123; CommandLine = $command }
$script:processes = @([pscustomobject]@{ SessionId = 2147483000; ProcessId = 456; CommandLine = 'chrome.exe' }, $owner)
Check 'only matching dedicated Chrome is adopted' ((Get-ProfileBrowser).Id -eq 123)
$owner.CommandLine = $command.Replace(' "--start-fullscreen"', '')
Check 'a dedicated window started without fullscreen is still adopted' ((Get-ProfileBrowser).Id -eq 123)
$owner.CommandLine = $command + ' --type=renderer'
Check 'renderer subprocess cannot own profile' ($null -eq (Get-ProfileBrowser))
$owner.CommandLine = $command.Replace('--remote-debugging-port=0', '--remote-debugging-port=9222')
Throws 'profile with incompatible startup flags is not taken over' { Get-ProfileBrowser }
$owner.CommandLine = $command.Replace(' "--disable-backgrounding-occluded-windows"', '')
Throws 'old browser must reopen before the background capture flags take effect' { Get-ProfileBrowser }
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
function New-Object {
    param($TypeName, [Parameter(ValueFromRemainingArguments = $true)] $Arguments)
    if ($TypeName -ne 'Threading.Mutex') { throw "Unexpected object in launcher lifecycle test: $TypeName" }
    Microsoft.PowerShell.Utility\New-Object Threading.Mutex($false, ('Local\PartyVisuals-Browser-Test-' + [guid]::NewGuid().ToString('N')))
}
function New-Item { param($ItemType, $Path, [switch] $Force) }
function Start-BrowserHelper { $script:events += 'helper-start'; return [pscustomobject]@{ HasExited = $false } }
function Stop-BrowserHelper { param($Process); if ($null -ne $Process) { $script:events += 'helper-stop' } }
function Start-Process { param($FilePath, $WorkingDirectory, $ArgumentList, [switch] $PassThru); $script:events += 'browser-start'; $script:starts += $script:tick; $script:launchArgs = $ArgumentList; $script:browser = New-FakeBrowser $script:nextStart; return $script:browser }
function Get-ProfileBrowser { if ($null -ne $script:adopt -and -not $script:adopt.HasExited) { return $script:adopt }; return $null }
function Get-DisplaySession { $script:session }
function Test-StopRequested { param($Session); return $script:tick -ge $script:stopAt }
function Start-Sleep { param($Seconds); $script:tick++; if ($script:tick -gt 1000) { throw 'The launcher loop did not stop.' }; & $script:onTick }
function Test-Path { param($LiteralPath, $PathType); return $true }
function Reset-Run([int] $StopAt = 1000) {
    $script:tick = 0; $script:stopAt = $StopAt; $script:events = @(); $script:starts = @(); $script:adopt = $null; $script:onTick = { }
    $script:nextStart = [datetime]::Now.AddMinutes(-10)
    $script:session = [pscustomobject]@{ Active = $true; Station = 'Console'; ConnectedAt = [datetime]::UtcNow.AddHours(-1); LogonAt = [datetime]::UtcNow.AddHours(-1) }
}
function Set-Session([bool] $Active, [string] $Station, [datetime] $ConnectedAt) {
    $script:session = [pscustomobject]@{ Active = $Active; Station = $Station; ConnectedAt = $ConnectedAt; LogonAt = $script:session.LogonAt }
}
Reset-Run
$previousProfile = $BrowserProfile
$BrowserProfile = Join-Path $env:LOCALAPPDATA 'Google\Chrome\User Data\Default'
Throws 'normal Chrome profile cannot be used by launcher' { Start-VisualsBrowser }
$BrowserProfile = $previousProfile
function Test-Path { param($LiteralPath, $PathType); return ($PathType -eq 'Leaf') }

Reset-Run -StopAt 3
$code = Start-VisualsBrowser
Check 'opens the browser once, runs the helper, and closes both on a stop request' ($code -eq 0 -and ($script:events -join ',') -eq 'browser-start,helper-start,browser-close,helper-stop')
Check 'launch carries no window geometry' ($script:launchArgs -notmatch '--window-(position|size)=')
Reset-Run -StopAt 0
$code = Start-VisualsBrowser
Check 'a stop request keeps the launcher from opening anything' ($code -eq 0 -and $script:events.Count -eq 0)
Reset-Run
$script:onTick = { if ($script:tick -eq 2) { $script:browser.HasExited = $true }; if ($script:starts.Count -ge 2) { $script:stopAt = $script:tick } }
[void](Start-VisualsBrowser)
Check 'a closed or crashed browser reopens 5 seconds later' ($script:starts.Count -eq 2 -and $script:starts[1] - 2 -eq 5)
Check 'the helper keeps running while the browser reopens' (@($script:events | Where-Object { $_ -eq 'helper-start' }).Count -eq 1)
Reset-Run
$script:nextStart = [datetime]::Now
$script:onTick = { if (-not $script:browser.HasExited) { $script:browser.HasExited = $true }; if ($script:starts.Count -ge 4) { $script:stopAt = $script:tick } }
[void](Start-VisualsBrowser)
Check 'a browser that keeps closing at once is reopened after 5, 10 and 20 seconds' (($script:starts[1] - $script:starts[0] - 1) -eq 5 -and ($script:starts[2] - $script:starts[1] - 1) -eq 10 -and ($script:starts[3] - $script:starts[2] - 1) -eq 20)
Reset-Run -StopAt 4
$script:onTick = { if ($script:tick -eq 2) { $script:browser.HasExited = $true } }
[void](Start-VisualsBrowser)
Check 'a stop request during the pause keeps the browser closed' (($script:events -join ',') -eq 'browser-start,helper-start,helper-stop')
Reset-Run
$script:onTick = { if ($script:tick -eq 2) { Set-Session $true 'Console' ([datetime]::UtcNow.AddMinutes(-5)); $script:nextStart = [datetime]::Now }; if ($script:starts.Count -ge 2) { $script:stopAt = $script:tick } }
[void](Start-VisualsBrowser)
Check 'a session connected after the browser started reopens it at once' (($script:events -join ',').StartsWith('browser-start,helper-start,browser-close,browser-start,') -and $script:starts[1] -eq 2)
Reset-Run
$script:session.Station = 'RDP-Tcp#3'
$script:onTick = { if ($script:tick -eq 2) { Set-Session $true 'Console' $script:session.ConnectedAt }; if ($script:starts.Count -ge 2) { $script:stopAt = $script:tick } }
[void](Start-VisualsBrowser)
Check 'a move from Remote Desktop to the console reopens the browser' (($script:events -join ',').StartsWith('browser-start,helper-start,browser-close,browser-start,') -and $script:starts[1] -eq 2)
Reset-Run -StopAt 6
$script:onTick = { if ($script:tick -eq 2) { Set-Session $true 'Console' ([datetime]::UtcNow) } }
[void](Start-VisualsBrowser)
Check 'a connection that is still settling leaves the browser running' ($script:starts.Count -eq 1 -and ($script:events -join ',') -eq 'browser-start,helper-start,browser-close,helper-stop')
Reset-Run -StopAt 6
$script:onTick = { if ($script:tick -eq 2) { Set-Session $false '' ([datetime]::UtcNow.AddMinutes(-5)) } }
[void](Start-VisualsBrowser)
Check 'a disconnected session leaves the browser running' ($script:starts.Count -eq 1)
Reset-Run -StopAt 3
$script:adopt = New-FakeBrowser
[void](Start-VisualsBrowser)
Check 'an already open dedicated browser is adopted, not relaunched' (($script:events -join ',') -eq 'helper-start,browser-close,helper-stop')
Reset-Run
$script:adopt = New-FakeBrowser ([datetime]::Now.AddHours(-2))
$script:nextStart = [datetime]::Now
$script:onTick = { if ($script:starts.Count -ge 1) { $script:stopAt = $script:tick } }
[void](Start-VisualsBrowser)
Check 'an adopted browser from before the session connected is reopened' (($script:events -join ',') -eq 'helper-start,browser-close,browser-start,browser-close,helper-stop')

$script:events = @()
$script:adopt = New-FakeBrowser
$script:stopFileAtClose = $false
$script:adopt | Add-Member -Force ScriptMethod CloseMainWindow { $script:stopFileAtClose = [IO.File]::Exists($StopFile); $script:events += 'browser-close'; $this.HasExited = $true; return $true }
Check '-Stop succeeds' ((Request-VisualsStop) -eq 0)
Check '-Stop records the stop before it closes the browser' ($script:stopFileAtClose -and [IO.File]::Exists($StopFile) -and ($script:events -join ',') -eq 'browser-close')
$script:startedTask = $null
function Start-ScheduledTask { param($TaskName); $script:startedTask = $TaskName }
Check '-Resume succeeds' ((Request-VisualsResume) -eq 0)
Check '-Resume clears the stop and starts the task' (-not [IO.File]::Exists($StopFile) -and $script:startedTask -eq $TaskName)
function Request-VisualsStop { 'stop-mode' }
$Stop = [switch]$true
Check 'a single management switch selects its mode' ((Invoke-BrowserLauncher) -eq 'stop-mode')
$Register = [switch]$true
Throws 'only one management switch is accepted at a time' { Invoke-BrowserLauncher }
$Register = [switch]$false
$Stop = [switch]$false
Write-Host "$script:passed passed, 0 failed"
