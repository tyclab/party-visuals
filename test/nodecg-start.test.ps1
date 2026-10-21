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
$launcherFile = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\install\Start-PartyVisualsNodecg.ps1'))
. $launcherFile
# A running NodeCG task owns the real stop file, log, folder and task name; the tests reach none of them.
$work = Join-Path ([IO.Path]::GetTempPath()) ('party-visuals-nodecg-' + [guid]::NewGuid().ToString('N'))
$NodecgRoot = Join-Path $work 'EclipseGraphics'
$LogFile = Join-Path $work 'logs\nodecg.log'
$StopFile = Join-Path $work 'nodecg.stop'
$TaskName = 'PartyVisuals NodeCG Test ' + [guid]::NewGuid().ToString('N')
$node = @(@($NodeExe) + @(Get-Command node.exe -CommandType Application -ErrorAction SilentlyContinue | ForEach-Object { $_.Path }) | Where-Object { $_ -and (Test-Path -LiteralPath $_ -PathType Leaf) })[0]
$NodeExe = $node
New-Item -ItemType Directory -Path $NodecgRoot | Out-Null

function Read-Log([string] $Path = $LogFile) {
    if (-not [IO.File]::Exists($Path)) { return '' }
    $stream = New-Object IO.FileStream($Path, [IO.FileMode]::Open, [IO.FileAccess]::Read, ([IO.FileShare]::ReadWrite -bor [IO.FileShare]::Delete))
    try { return (New-Object IO.StreamReader($stream, [Text.Encoding]::UTF8)).ReadToEnd() } finally { $stream.Dispose() }
}
function Wait-Log([string] $Pattern, [int] $Count = 1, [int] $Seconds = 30) {
    $until = [datetime]::UtcNow.AddSeconds($Seconds)
    while ([datetime]::UtcNow -lt $until) {
        if ([regex]::Matches((Read-Log), $Pattern).Count -ge $Count) { return $true }
        Microsoft.PowerShell.Utility\Start-Sleep -Milliseconds 200
    }
    return $false
}
function Get-StandInProcesses {
    @(Get-CimInstance Win32_Process -Filter "Name='node.exe'" | Where-Object { $_.CommandLine -and $_.CommandLine.IndexOf($NodecgRoot, [StringComparison]::OrdinalIgnoreCase) -ge 0 })
}
function Set-StandIn([string] $Mode) {
    [IO.File]::WriteAllText((Join-Path $NodecgRoot 'mode.txt'), $Mode)
    if (Test-Path -LiteralPath (Join-Path $NodecgRoot 'runs.txt')) { Remove-Item -LiteralPath (Join-Path $NodecgRoot 'runs.txt') }
}
[IO.File]::WriteAllText((Join-Path $NodecgRoot 'start.js'), @'
const fs = require('fs'), path = require('path');
const mode = fs.readFileSync(path.join(__dirname, 'mode.txt'), 'utf8').trim();
fs.appendFileSync(path.join(__dirname, 'runs.txt'), 'run\n');
const runs = fs.readFileSync(path.join(__dirname, 'runs.txt'), 'utf8').trim().split('\n').length;
console.log('stand-in up \u00e9 \x1b[32mgreen\x1b[0m run ' + runs);
console.error('stand-in warning');
if (mode === 'crash') process.exit(3);
if (mode === 'eacces') { console.error('\x1b[31merror\x1b[39m [nodecg/lib/server] Unhandled error! Error: listen EACCES: permission denied 127.0.0.1:9090'); process.exit(1); }
if (mode === 'inuse') { console.log('error [nodecg/lib/server] Listen 127.0.0.1:9090 in use, is NodeCG already running? NodeCG will now exit.'); process.exit(1); }
if (mode.startsWith('listen:')) require('http').createServer().on('error', (err) => { console.error('Unhandled error!', err); process.exit(1); }).listen(Number(mode.slice(7)), '127.0.0.1');
if (mode === 'once' && runs === 1) setTimeout(() => process.exit(0), 300);
if (mode === 'worker') {
  const { Worker } = require('worker_threads');
  new Worker("setTimeout(() => require('worker_threads').parentPort.postMessage('alive'), 1500)", { eval: true }).on('message', (m) => console.log('worker ' + m));
}
if (mode === 'stuck') process.on('SIGINT', () => { console.log('hanging'); for (;;) {} });
else if (mode !== 'plain') process.on('SIGINT', () => { console.log('saving state'); setTimeout(() => process.exit(0), 100); });
setInterval(() => {}, 1000);
'@)

try {
    Check 'node.exe is there for the process tests' ($null -ne $node)
    Throws 'arguments reject line breaks' { ConvertTo-NativeArgument "a`nb" }
    $nodeArgs = @(Get-NodecgArguments)
    Check 'NodeCG runs start.js with the stop hook preloaded' ($nodeArgs.Count -eq 3 -and $nodeArgs[0] -eq '--import' -and $nodeArgs[2] -eq (Join-Path $NodecgRoot 'start.js'))
    Check 'the stop hook travels intact as a data URL' ($nodeArgs[1].StartsWith('data:text/javascript,') -and [uri]::UnescapeDataString($nodeArgs[1].Substring(21)) -ceq $StopHook)
    Check 'the command line carries no configuration or token' (($nodeArgs -join ' ') -notmatch 'token|cfg')

    $mutexName = Get-LauncherMutexName
    $sameFolder = & { $NodecgRoot = $NodecgRoot.ToUpperInvariant() + '\'; Get-LauncherMutexName }
    $otherFolder = & { $NodecgRoot = $NodecgRoot + '2'; Get-LauncherMutexName }
    Check 'one launcher name per NodeCG folder, whatever its spelling' ($mutexName -like 'Local\PartyVisuals-NodeCG-*' -and $sameFolder -eq $mutexName -and $otherFolder -ne $mutexName)

    Initialize-NodecgNative
    Check 'the session record matches the Windows WTSINFOW layout' ([Runtime.InteropServices.Marshal]::SizeOf([type]'PartyNodecgNative+SessionInfo') -eq 216)
    $signIn = [PartyNodecgNative]::SignInUtc()
    Check 'this session reports its sign-in time' ($signIn -gt [datetime]'2000-01-01' -and $signIn -le [datetime]::UtcNow)

    $small = Join-Path $work 'rotate\test.log'
    $log = New-Object PartyNodecgLog($small, 400)
    for ($i = 0; $i -lt 12; $i++) { $log.Write('stdout', "line $i " + ('x' * 40)) }
    Check 'the log rotates into one older file at its size cap' ([IO.File]::Exists("$small.1") -and (Get-Item -LiteralPath $small).Length -le 400 -and (Get-Item -LiteralPath "$small.1").Length -le 400 -and @(Get-ChildItem -LiteralPath (Split-Path $small)).Count -eq 2)
    Check 'the newest line is in the current file, the one before in the older' ((Read-Log $small) -match 'line 11 ' -and (Read-Log "$small.1") -match 'line \d+ ' -and (Read-Log $small) -notmatch 'line 0 ')
    $log.Write('stderr', ([char]27 + '[31mred' + [char]27 + '[0m caf' + [char]0xE9))
    $last = @((Read-Log $small).Trim() -split "`r`n")[-1]
    Check 'a line carries its UTC time and source, without colour codes' ($last -match '^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z stderr red caf\u00e9$')
    $log.Write('stdout', 'y' * 10000)
    Check 'an overlong line is cut' (@((Read-Log $small).Trim() -split "`r`n")[-1].Length -lt 4100)
    New-Item -ItemType Directory -Path (Join-Path $work 'blocked\x.log') -Force | Out-Null
    $blocked = New-Object PartyNodecgLog((Join-Path $work 'blocked\x.log'), 400)
    $quiet = $true
    try { $blocked.Write('stdout', 'nowhere to go') } catch { $quiet = $false }
    Check 'a log that cannot be written is skipped without an error' $quiet

    $runLog = New-Object PartyNodecgLog($LogFile, $LogLimitBytes)
    Set-StandIn 'crash'
    $run = Start-NodecgProcess $runLog
    Check 'a crashing NodeCG exits with its code' ($run.Process.WaitForExit(15000) -and $run.Drain(5000) -and $run.Process.ExitCode -eq 3)
    $text = Read-Log
    Check 'NodeCG output reaches the log, UTF-8 intact and colour codes dropped' ($text -match 'stdout stand-in up \u00e9 green run 1' -and $text -match 'stderr stand-in warning' -and $text -match 'launcher NodeCG started \(pid \d+\)')
    Check 'an exit reads with its time and code' ((Get-ExitText $run.Process) -match '^exited at \d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z with code 3 \(0x00000003\)$')
    Check 'an ordinary crash is not taken for a port failure' ($null -eq $run.PortError)
    $run.Process.Dispose()

    Set-StandIn 'stay'
    $run = Start-NodecgProcess $runLog
    Check 'the stand-in comes up' (Wait-Log 'stand-in up \u00e9 green run 1' 2)
    $before = [datetime]::UtcNow
    Stop-NodecgProcess $run $runLog
    $text = Read-Log
    Check 'a stop closes standard input and NodeCG saves and exits with 0' ($text -match 'stdout saving state' -and $text -match 'NodeCG stopped: exited at \S+ with code 0 \(0x00000000\)' -and $text -notmatch 'did not shut down')
    Check 'the stop takes seconds, not the kill timeout' (([datetime]::UtcNow - $before).TotalSeconds -lt 10)

    Set-StandIn 'plain'
    $run = Start-NodecgProcess $runLog
    [void](Wait-Log 'stand-in up \u00e9 green run 1' 3)
    $run.Process.StandardInput.Close()
    Check 'without a Ctrl+C handler the hook exits with 0 at once' ($run.Process.WaitForExit(5000) -and $run.Process.ExitCode -eq 0)
    $run.Process.Dispose()

    Set-StandIn 'worker'
    $run = Start-NodecgProcess $runLog
    Check 'the hook leaves worker threads running' (Wait-Log 'stdout worker alive' 1 15)
    Stop-NodecgProcess $run $runLog

    $warnings = @(& {
        Set-StandIn 'stay'
        $outside = Start-NodecgProcess $runLog
        [void](Wait-Log 'stand-in up \u00e9 green run 1' 5)
        try { Check '-Stop without a launcher returns at once' ((Request-NodecgStop) -eq 0) } finally { Stop-NodecgProcess $outside $runLog }
    } 3>&1 | Where-Object { $_ -is [Management.Automation.WarningRecord] })
    Check '-Stop names a NodeCG that runs outside the launcher' ($warnings.Count -eq 1 -and $warnings[0].Message -match 'outside this launcher \(pid \d+\)')
    Check '-Stop records the stop' ([IO.File]::Exists($StopFile))
    [IO.File]::Delete($StopFile)

    Set-StandIn 'stuck'
    $run = Start-NodecgProcess $runLog
    [void](Wait-Log 'stand-in up \u00e9 green run 1' 6)
    $ShutdownSeconds = 2
    Stop-NodecgProcess $run $runLog
    $ShutdownSeconds = 20
    $text = Read-Log
    Check 'a NodeCG that hangs on shutdown is ended after the wait' ($text -match 'stdout hanging' -and $text -match 'did not shut down within 2 seconds; ending it\.' -and $text -match 'NodeCG stopped: exited at \S+ with code -1 \(0xFFFFFFFF\)')

    $holder = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
    $holder.Start()
    $heldPort = $holder.LocalEndpoint.Port
    try {
        Set-StandIn "listen:$heldPort"
        $run = Start-NodecgProcess $runLog
        Check 'a NodeCG whose port is taken exits with EADDRINUSE on that port' ($run.Process.WaitForExit(15000) -and $run.Drain(5000) -and $run.Process.ExitCode -eq 1 -and $run.PortError -eq 'EADDRINUSE' -and $run.Port -eq "$heldPort")
        Check 'the advice names the port, its holder and the reserved ranges' ((Get-PortAdvice $run) -ceq "NodeCG could not listen on port $heldPort (EADDRINUSE: in use; see Get-NetTCPConnection -LocalPort $heldPort); check netsh int ipv4 show excludedportrange protocol=tcp and netsh int ipv4 show dynamicport tcp.")
        $run.Process.Dispose()
    } finally { $holder.Stop() }
    Set-StandIn 'eacces'
    $run = Start-NodecgProcess $runLog
    Check 'listen EACCES is read with its port, colour codes and all' ($run.Process.WaitForExit(15000) -and $run.Drain(5000) -and $run.PortError -eq 'EACCES' -and $run.Port -eq '9090')
    Check 'the EACCES advice points at the reserved port ranges' ((Get-PortAdvice $run) -ceq 'NodeCG could not listen on port 9090 (EACCES: reserved or blocked); check netsh int ipv4 show excludedportrange protocol=tcp and netsh int ipv4 show dynamicport tcp.')
    $run.Process.Dispose()
    Set-StandIn 'inuse'
    $run = Start-NodecgProcess $runLog
    Check 'NodeCG''s own port-in-use message reads as EADDRINUSE' ($run.Process.WaitForExit(15000) -and $run.Drain(5000) -and $run.PortError -eq 'EADDRINUSE' -and $run.Port -eq '9090')
    $run.Process.Dispose()

    $hostExe = "$env:SystemRoot\System32\conhost.exe"
    $taskLine = (Get-TaskArguments @{ NodeExe = $NodeExe; NodecgRoot = $NodecgRoot; LogFile = $LogFile; StopFile = $StopFile }) -join ' '
    [IO.File]::Delete($LogFile)
    Set-StandIn 'once'
    $first = Start-Process -FilePath $hostExe -ArgumentList $taskLine -PassThru
    $restarted = Wait-Log 'stand-in up \u00e9 green run 2' 1 30
    $text = Read-Log
    Check 'the task command line runs the launcher, which starts NodeCG' ($text -match 'launcher Launcher started \(pid \d+\)' -and $text -match 'stand-in up \u00e9 green run 1')
    Check 'NodeCG that exits with 0 is restarted 5 seconds later' ($restarted -and $text -match 'NodeCG exited at \S+ with code 0 \(0x00000000\) after \d+ s; restarting in 5 s\.' -and $text -match 'stand-in up \u00e9 green run 2')
    $second = Start-Process -FilePath $hostExe -ArgumentList $taskLine -PassThru
    Check 'a second launcher for the same folder ends at once' ($second.WaitForExit(20000) -and [regex]::Matches((Read-Log), 'Launcher started').Count -eq 1)
    Check '-Stop returns once the launcher has stopped NodeCG' ((Request-NodecgStop) -eq 0 -and (Read-Log) -match 'NodeCG stopped: exited' -and $first.WaitForExit(10000))
    $text = Read-Log
    Check '-Stop lets NodeCG save, then the launcher ends it with 0' ($text -match '(?s)Stop requested \(-Stop\)\..*stdout saving state.*NodeCG stopped: exited at \S+ with code 0' -and @(Get-StandInProcesses).Count -eq 0)
    $third = Start-Process -FilePath $hostExe -ArgumentList $taskLine -PassThru
    Check 'a stop made in this sign-in keeps the next start from running NodeCG' ($third.WaitForExit(20000) -and [regex]::Matches((Read-Log), 'NodeCG started').Count -eq 2)
    [IO.File]::Delete($StopFile)

    Set-StandIn 'stay'
    $taskHost = Start-Process -FilePath $hostExe -ArgumentList $taskLine -PassThru
    [void](Wait-Log 'stand-in up \u00e9 green run 1' 3 30)
    # Ending the host process is what Task Scheduler does when the task is stopped.
    Stop-Process -Id $taskHost.Id -Force
    Check 'when the task ends, the launcher lets NodeCG save and exit' ((Wait-Log '(?s)The task was ended\..*NodeCG stopped: exited at \S+ with code 0' 1 30) -and (Read-Log) -match 'saving state')
    Microsoft.PowerShell.Utility\Start-Sleep -Seconds 1
    Check 'nothing of the ended task keeps running' (@(Get-StandInProcesses).Count -eq 0 -and @(Get-CimInstance Win32_Process -Filter "Name='powershell.exe'" | Where-Object { $_.CommandLine -and $_.CommandLine.Contains($StopFile) }).Count -eq 0)

    $previous = [pscustomobject]@{ LogonAt = [datetime]::UtcNow.AddHours(-1) }
    Check 'no stop file, no stop' (-not (Test-StopRequested $previous.LogonAt))
    [IO.File]::WriteAllText($StopFile, 'stop')
    Check 'a stop made during this sign-in holds' (Test-StopRequested $previous.LogonAt)
    [IO.File]::SetLastWriteTimeUtc($StopFile, [datetime]::UtcNow.AddHours(-2))
    Check 'a stop from an earlier sign-in is dropped and removed' (-not (Test-StopRequested $previous.LogonAt) -and -not [IO.File]::Exists($StopFile))
    [IO.File]::WriteAllText($StopFile, 'stop')
    [IO.File]::SetLastWriteTimeUtc($StopFile, [datetime]::UtcNow.AddHours(-2))
    Check 'a stop holds when the sign-in time is unknown' (Test-StopRequested ([datetime]::MinValue))
    [IO.File]::Delete($StopFile)

    Check 'the first restart waits 5 seconds' ((Get-RelaunchDelay 0 ([timespan]::FromSeconds(2))) -eq 5)
    Check 'a NodeCG that keeps exiting at once waits twice as long each time' ((Get-RelaunchDelay 5 ([timespan]::FromSeconds(2))) -eq 10 -and (Get-RelaunchDelay 40 ([timespan]::FromSeconds(59))) -eq 80)
    Check 'the pause between restarts is capped at 5 minutes' ((Get-RelaunchDelay 160 ([timespan]::FromSeconds(2))) -eq 300 -and (Get-RelaunchDelay 300 ([timespan]::FromSeconds(2))) -eq 300)
    Check 'a NodeCG that fails just after its minute-long start still backs off' ((Get-RelaunchDelay 5 ([timespan]::FromSeconds(65))) -eq 10 -and (Get-RelaunchDelay 160 ([timespan]::FromMinutes(9))) -eq 300)
    Check 'a NodeCG that stayed up for ten minutes restarts after 5 seconds again' ((Get-RelaunchDelay 300 ([timespan]::FromMinutes(10))) -eq 5)

    Check 'the launcher knows its own file for the task' ($LauncherPath -eq $launcherFile)
    $script:registered = $null
    function Register-ScheduledTask { param($TaskName, $Xml, [switch] $Force); $script:registered = @{ Name = $TaskName; Xml = $Xml; Force = [bool]$Force } }
    $bound = @{ Register = [switch]$true; Stop = [switch]$false; TaskName = 'other'; Verbose = [switch]$true; NodeExe = 'C:\Program Files\nodejs\node.exe'; NodecgRoot = 'C:\Users\Test User\P&V\EclipseGraphics'; LogFile = 'C:\Users\Test User\PV\logs\nodecg.log'; StopFile = 'C:\Users\Test User\PV\nodecg.stop' }
    Check 'registration succeeds' ((Register-NodecgTask $bound) -eq 0)
    $task = ([xml]$script:registered.Xml).Task
    $user = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    Check 'registration replaces the task of the configured name' ($script:registered.Name -eq $TaskName -and $script:registered.Force)
    Check 'the task starts at this user''s logon only' ($task.Triggers.LogonTrigger.UserId -eq $user -and @($task.Triggers.ChildNodes).Count -eq 1)
    Check 'the task ignores duplicates, has no time limit and retries failed starts every minute' ($task.Settings.MultipleInstancesPolicy -eq 'IgnoreNew' -and $task.Settings.ExecutionTimeLimit -eq 'PT0S' -and $task.Settings.StartWhenAvailable -eq 'true' -and $task.Settings.RestartOnFailure['Count'].InnerText -eq '999' -and $task.Settings.RestartOnFailure['Interval'].InnerText -eq 'PT1M')
    Check 'the task runs at normal priority, not the Task Scheduler default below normal' ($task.Settings.Priority -eq '5')
    Check 'the task runs on this user''s interactive desktop with limited rights' ($task.Principals.Principal.UserId -eq $user -and $task.Principals.Principal.LogonType -eq 'InteractiveToken' -and $task.Principals.Principal.RunLevel -eq 'LeastPrivilege')
    Check 'the task runs this launcher in a headless console host' ($task.Actions.Exec.Command -eq "$env:SystemRoot\System32\conhost.exe" -and $task.Actions.Exec.WorkingDirectory -eq (Split-Path -Parent $launcherFile))
    Check 'the task keeps the launch options but not the management switches' ($task.Actions.Exec.Arguments -ceq ('--headless "' + "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe" + '" -NoProfile -ExecutionPolicy Bypass -File "' + $launcherFile + '" -LogFile "C:\Users\Test User\PV\logs\nodecg.log" -NodecgRoot "C:\Users\Test User\P&V\EclipseGraphics" -NodeExe "C:\Program Files\nodejs\node.exe" -StopFile "C:\Users\Test User\PV\nodecg.stop"'))
    $scheduler = New-Object -ComObject Schedule.Service
    $scheduler.Connect()
    $validated = $true
    try { [void]$scheduler.GetFolder('\').RegisterTask($TaskName, $script:registered.Xml, 1, $null, $null, 3) } catch { $validated = $false; Write-Host $_.Exception.Message }
    Check 'Task Scheduler accepts the task definition (validation only)' $validated
    Check 'the tests registered no real task' (-not (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue))

    function Initialize-NodecgNative { }
    function New-FakeRun {
        $process = [pscustomobject]@{ HasExited = $false; ExitCode = 0; ExitTime = [datetime]::Now; Id = 1 }
        $process | Add-Member ScriptMethod Dispose { }
        $fake = [pscustomobject]@{ Process = $process; PortError = $null; Port = $null }
        $fake | Add-Member ScriptMethod Drain { param($Milliseconds); return $true }
        $fake
    }
    function Start-NodecgProcess { param($Log); if ($script:failStarts-- -gt 0) { throw 'no such file' }; $script:events += 'start'; $script:starts += $script:tick; $script:run = New-FakeRun; return $script:run }
    function Stop-NodecgProcess { param($Run, $Log); if ($null -ne $Run) { $script:events += 'stop' } }
    function Get-TaskHost { $script:taskHost }
    function Test-StopRequested { param($SignIn); return $script:tick -ge $script:stopAt }
    function Start-Sleep { param($Seconds); $script:tick++; if ($script:tick -gt 1000) { throw 'The launcher loop did not stop.' }; & $script:onTick }
    function Test-Path { param($LiteralPath, $PathType); return $true }
    function Reset-Run([int] $StopAt = 1000) {
        $script:tick = 0; $script:stopAt = $StopAt; $script:events = @(); $script:starts = @(); $script:onTick = { }; $script:taskHost = $null; $script:failStarts = 0
        [IO.File]::Delete($LogFile)
    }
    Reset-Run -StopAt 3
    Check 'starts NodeCG once and stops it on a stop request' ((Start-NodecgSupervisor) -eq 0 -and ($script:events -join ',') -eq 'start,stop' -and (Read-Log) -match 'Stop requested')
    Reset-Run -StopAt 0
    Check 'a stop request keeps the launcher from starting NodeCG' ((Start-NodecgSupervisor) -eq 0 -and $script:events.Count -eq 0)
    Reset-Run
    $script:onTick = { if ($script:tick -eq 2) { $script:run.Process.ExitCode = -1073741510; $script:run.Process.HasExited = $true }; if ($script:starts.Count -ge 2) { $script:stopAt = $script:tick } }
    [void](Start-NodecgSupervisor)
    Check 'NodeCG that exits for any reason is started again 5 seconds later' ($script:starts.Count -eq 2 -and $script:starts[1] - 2 -eq 5)
    Check 'each exit is logged with its code in hex' ((Read-Log) -match 'NodeCG exited at \S+ with code -1073741510 \(0xC000013A\) after \d+ s; restarting in 5 s\.')
    Reset-Run
    $script:onTick = { if (-not $script:run.Process.HasExited) { $script:run.Process.HasExited = $true }; if ($script:starts.Count -ge 4) { $script:stopAt = $script:tick } }
    [void](Start-NodecgSupervisor)
    Check 'a NodeCG that keeps exiting at once is started after 5, 10 and 20 seconds' (($script:starts[1] - $script:starts[0] - 1) -eq 5 -and ($script:starts[2] - $script:starts[1] - 1) -eq 10 -and ($script:starts[3] - $script:starts[2] - 1) -eq 20)
    Reset-Run
    $script:onTick = { if (-not $script:run.Process.HasExited) { $script:run.PortError = 'EACCES'; $script:run.Port = '9090'; $script:run.Process.ExitCode = 1; $script:run.Process.HasExited = $true }; if ($script:starts.Count -ge 4) { $script:stopAt = $script:tick } }
    [void](Start-NodecgSupervisor)
    $text = Read-Log
    Check 'every port failure is logged as such, and NodeCG is retried with growing pauses' ($script:starts.Count -eq 4 -and [regex]::Matches($text, 'launcher NodeCG could not listen on port 9090 \(EACCES').Count -eq 3 -and $text -match '(?s)port 9090 \(EACCES.*code 1 \(0x00000001\) after \d+ s; restarting in 20 s\.')
    Reset-Run -StopAt 4
    $script:onTick = { if ($script:tick -eq 2) { $script:run.Process.HasExited = $true } }
    [void](Start-NodecgSupervisor)
    Check 'a stop request during the pause keeps NodeCG stopped' (($script:events -join ',') -eq 'start')
    Reset-Run
    $script:taskHost = [pscustomobject]@{ HasExited = $false }
    $script:onTick = { if ($script:tick -eq 2) { $script:taskHost.HasExited = $true } }
    Check 'when the task host ends, the launcher stops NodeCG and ends' ((Start-NodecgSupervisor) -eq 0 -and ($script:events -join ',') -eq 'start,stop' -and (Read-Log) -match 'The task was ended')
    Reset-Run
    $script:failStarts = 1
    $script:onTick = { if ($script:starts.Count -ge 1) { $script:stopAt = $script:tick } }
    [void](Start-NodecgSupervisor)
    Check 'a start that fails is retried after the pause' ($script:starts.Count -eq 1 -and $script:starts[0] -eq 5 -and (Read-Log) -match 'NodeCG could not start: no such file; retrying in 5 s')
    function Test-Path { param($LiteralPath, $PathType); return $false }
    Throws 'a missing node.exe or start.js stops the launcher with an error' { Start-NodecgSupervisor }

    $script:startedTask = $null
    function Start-ScheduledTask { param($TaskName); $script:startedTask = $TaskName }
    [IO.File]::WriteAllText($StopFile, 'stop')
    Check '-Resume succeeds' ((Request-NodecgResume) -eq 0)
    Check '-Resume clears the stop and starts the task' (-not [IO.File]::Exists($StopFile) -and $script:startedTask -eq $TaskName)
    function Request-NodecgStop { 'stop-mode' }
    $Stop = [switch]$true
    Check 'a single management switch selects its mode' ((Invoke-NodecgLauncher) -eq 'stop-mode')
    $Register = [switch]$true
    Throws 'only one management switch is accepted at a time' { Invoke-NodecgLauncher }
    $Register = [switch]$false
    $Stop = [switch]$false
} finally {
    Get-StandInProcesses | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
Write-Host "$script:passed passed, 0 failed"
