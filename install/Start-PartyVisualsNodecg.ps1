#Requires -Version 5.1
[CmdletBinding()]
param(
    [string] $NodeExe = "$env:ProgramFiles\nodejs\node.exe",
    [string] $NodecgRoot = "$env:LOCALAPPDATA\PartyVisuals\EclipseGraphics",
    [string] $LogFile = "$env:LOCALAPPDATA\PartyVisuals\logs\nodecg.log",
    [string] $StopFile = "$env:LOCALAPPDATA\PartyVisuals\nodecg.stop",
    [string] $TaskName = 'PartyVisuals NodeCG',
    [switch] $Stop,
    [switch] $Resume,
    [switch] $Register
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$LauncherPath = $PSCommandPath
$LaunchParameters = $PSBoundParameters
$LogLimitBytes = 1MB
$ShutdownSeconds = 20
# Closing NodeCG's standard input works like Ctrl+C in its console: NodeCG saves its state and exits. Also when the launcher dies.
$StopHook = "process.stdin.on('end',()=>{setTimeout(()=>process.exit(1),15e3).unref();process.listenerCount('SIGINT')?process.emit('SIGINT'):process.exit(0)}).resume()"

function Initialize-NodecgNative {
    if ('PartyNodecgRun' -as [type]) { return }
    Add-Type @'
using System;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
public sealed class PartyNodecgLog {
 readonly object gate = new object();
 readonly string path; readonly long limit;
 public PartyNodecgLog(string path, long limit) {
  this.path = path; this.limit = limit;
  try { Directory.CreateDirectory(Path.GetDirectoryName(path)); } catch (Exception) { }
 }
 public static string Clean(string text) { return Regex.Replace(text ?? "", "\x1b\\[[0-9;?]*[ -/]*[@-~]", ""); }
 public void Write(string source, string text) {
  text = Clean(text);
  if (text.Length > 4000) text = text.Substring(0, 4000) + "...";
  var bytes = Encoding.UTF8.GetBytes(DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ", CultureInfo.InvariantCulture) + " " + source + " " + text + "\r\n");
  lock (gate) {
   try {
    var current = new FileInfo(path);
    if (current.Exists && current.Length + bytes.Length > limit) { File.Delete(path + ".1"); File.Move(path, path + ".1"); }
    using (var file = new FileStream(path, FileMode.Append, FileAccess.Write, FileShare.ReadWrite | FileShare.Delete)) file.Write(bytes, 0, bytes.Length);
   } catch (Exception) { }
  }
 }
}
public sealed class PartyNodecgRun {
 static readonly Regex PortFailure = new Regex(@"\blisten (EACCES|EADDRINUSE)\b.*:(\d{1,5})\b|\bListen \S*:(\d{1,5}) in use\b");
 public readonly Process Process;
 public volatile string PortError, Port;
 readonly ManualResetEvent outputDone = new ManualResetEvent(false), errorDone = new ManualResetEvent(false);
 public PartyNodecgRun(string file, string arguments, string directory, PartyNodecgLog log) {
  var info = new ProcessStartInfo(file, arguments);
  info.WorkingDirectory = directory; info.UseShellExecute = false; info.CreateNoWindow = true;
  info.RedirectStandardInput = true; info.RedirectStandardOutput = true; info.RedirectStandardError = true;
  info.StandardOutputEncoding = Encoding.UTF8; info.StandardErrorEncoding = Encoding.UTF8;
  Process = new Process(); Process.StartInfo = info;
  Process.OutputDataReceived += (s, e) => { if (e.Data == null) outputDone.Set(); else log.Write("stdout", Inspect(e.Data)); };
  Process.ErrorDataReceived += (s, e) => { if (e.Data == null) errorDone.Set(); else log.Write("stderr", Inspect(e.Data)); };
  if (!Process.Start()) throw new InvalidOperationException("NodeCG did not start.");
  Process.BeginOutputReadLine(); Process.BeginErrorReadLine();
 }
 public bool Drain(int milliseconds) { return outputDone.WaitOne(milliseconds) && errorDone.WaitOne(milliseconds); }
 string Inspect(string line) {
  line = PartyNodecgLog.Clean(line);
  var found = PortFailure.Match(line);
  if (found.Success) { PortError = found.Groups[1].Success ? found.Groups[1].Value : "EADDRINUSE"; Port = found.Groups[1].Success ? found.Groups[2].Value : found.Groups[3].Value; }
  return line;
 }
}
public static class PartyNodecgNative {
 [DllImport("wtsapi32.dll", SetLastError=true)] static extern bool WTSQuerySessionInformationW(IntPtr server, int session, int infoClass, out IntPtr buffer, out int bytes);
 [DllImport("wtsapi32.dll")] static extern void WTSFreeMemory(IntPtr memory);
 [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] public struct SessionInfo {
  public int State, Id, InBytes, OutBytes, InFrames, OutFrames, InCompressed, OutCompressed;
  [MarshalAs(UnmanagedType.ByValTStr, SizeConst=32)] public string Station;
  [MarshalAs(UnmanagedType.ByValTStr, SizeConst=17)] public string Domain;
  [MarshalAs(UnmanagedType.ByValTStr, SizeConst=21)] public string User;
  public long ConnectTime, DisconnectTime, LastInputTime, LogonTime, CurrentTime;
 }
 public static DateTime SignInUtc() {
  IntPtr memory; int bytes;
  if (!WTSQuerySessionInformationW(IntPtr.Zero, -1, 24, out memory, out bytes)) return DateTime.MinValue;
  try { return DateTime.FromFileTimeUtc(((SessionInfo)Marshal.PtrToStructure(memory, typeof(SessionInfo))).LogonTime); } finally { WTSFreeMemory(memory); }
 }
}
'@
}

function ConvertTo-NativeArgument([string] $Value) {
    if ($Value -match '[\r\n\x00]') { throw 'Process arguments cannot contain line breaks or NUL.' }
    return '"' + [regex]::Replace([regex]::Replace($Value, '(\\*)"', '$1$1\"'), '(\\+)$', '$1$1') + '"'
}

function Get-NodecgArguments {
    '--import'; 'data:text/javascript,' + [uri]::EscapeDataString($StopHook)
    Join-Path $NodecgRoot 'start.js'
}

function Get-LauncherMutexName {
    # One launcher per NodeCG folder; -Stop waits on the same name.
    $folder = [IO.Path]::GetFullPath($NodecgRoot).TrimEnd('\').ToLowerInvariant()
    $sha = [Security.Cryptography.SHA256]::Create()
    try { $digest = $sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($folder)) } finally { $sha.Dispose() }
    'Local\PartyVisuals-NodeCG-' + (-join ($digest[0..7] | ForEach-Object { $_.ToString('x2') }))
}

function Start-NodecgProcess($Log) {
    $line = ((Get-NodecgArguments | ForEach-Object { ConvertTo-NativeArgument $_ }) -join ' ')
    $run = New-Object PartyNodecgRun($NodeExe, $line, $NodecgRoot, $Log)
    $Log.Write('launcher', "NodeCG started (pid $($run.Process.Id)): $(Join-Path $NodecgRoot 'start.js')")
    return $run
}

function Get-ExitText($Process) {
    'exited at {0}Z with code {1} (0x{1:X8})' -f $Process.ExitTime.ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ss.fff', [Globalization.CultureInfo]::InvariantCulture), $Process.ExitCode
}

function Get-PortAdvice($Run) {
    # Windows reserves port blocks inside its dynamic range; a range that reaches 9090 makes NodeCG's listen fail.
    $check = 'check netsh int ipv4 show excludedportrange protocol=tcp and netsh int ipv4 show dynamicport tcp'
    if ($Run.PortError -eq 'EACCES') { return "NodeCG could not listen on port $($Run.Port) (EACCES: reserved or blocked); $check." }
    "NodeCG could not listen on port $($Run.Port) (EADDRINUSE: in use; see Get-NetTCPConnection -LocalPort $($Run.Port)); $check."
}

function Stop-NodecgProcess($Run, $Log) {
    if ($null -eq $Run) { return }
    try {
        if (-not $Run.Process.HasExited) {
            try { $Run.Process.StandardInput.Close() } catch { }
            if (-not $Run.Process.WaitForExit($ShutdownSeconds * 1000)) {
                $Log.Write('launcher', "NodeCG did not shut down within $ShutdownSeconds seconds; ending it.")
                $Run.Process.Kill()
                [void]$Run.Process.WaitForExit(5000)
            }
        }
        [void]$Run.Drain(2000)
        $Log.Write('launcher', "NodeCG stopped: $(Get-ExitText $Run.Process)")
    } finally { $Run.Process.Dispose() }
}

function Get-TaskHost {
    # Under the task the parent is the headless console host; Task Scheduler ends that host when the task is stopped.
    $id = (Get-CimInstance Win32_Process -Filter "ProcessId=$PID").ParentProcessId
    $parent = Get-Process -Id $id -ErrorAction SilentlyContinue
    if ($null -eq $parent -or $parent.ProcessName -ne 'conhost') { return $null }
    $null = $parent.Handle
    return $parent
}

function Test-StopRequested([datetime] $SignIn) {
    $file = [IO.FileInfo]::new($StopFile)
    if (-not $file.Exists) { return $false }
    # A stop holds for the sign-in it was made in, so a restart or a new sign-in brings NodeCG back.
    if ($file.LastWriteTimeUtc -ge $SignIn) { return $true }
    try { $file.Delete() } catch { }
    return $false
}

function Get-RelaunchDelay([int] $Previous, [timespan] $Uptime) {
    # NodeCG takes about a minute to start, so only ten minutes up count as recovered.
    if ($Uptime.TotalMinutes -ge 10) { return 5 }
    return [Math]::Min(300, [Math]::Max(5, 2 * $Previous))
}

function Start-NodecgSupervisor {
    foreach ($path in @($NodeExe, (Join-Path $NodecgRoot 'start.js'))) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Required file is missing: $path" }
    }
    Initialize-NodecgNative
    $log = New-Object PartyNodecgLog($LogFile, $LogLimitBytes)
    $mutex = New-Object Threading.Mutex($false, (Get-LauncherMutexName))
    $owned = $false
    $nodecg = $null
    try {
        try { $owned = $mutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $owned = $true }
        if (-not $owned) { return 0 }
        $taskHost = Get-TaskHost
        $signIn = [PartyNodecgNative]::SignInUtc()
        $startedAt = [datetime]::MinValue
        $delay = 0
        $wait = 0
        $log.Write('launcher', "Launcher started (pid $PID).")
        while ($true) {
            if (Test-StopRequested $signIn) { $log.Write('launcher', 'Stop requested (-Stop).'); return 0 }
            if ($null -ne $taskHost -and $taskHost.HasExited) { $log.Write('launcher', 'The task was ended.'); return 0 }
            if ($null -ne $nodecg -and $nodecg.Process.HasExited) {
                [void]$nodecg.Drain(2000)
                $uptime = [datetime]::UtcNow - $startedAt
                $delay = Get-RelaunchDelay $delay $uptime
                $wait = $delay
                if ($nodecg.PortError) { $log.Write('launcher', (Get-PortAdvice $nodecg)) }
                $log.Write('launcher', ('NodeCG {0} after {1} s; restarting in {2} s.' -f (Get-ExitText $nodecg.Process), [long]$uptime.TotalSeconds, $delay))
                $nodecg.Process.Dispose()
                $nodecg = $null
            }
            if ($null -eq $nodecg -and $wait -eq 0) {
                try { $nodecg = Start-NodecgProcess $log; $startedAt = [datetime]::UtcNow }
                catch { $delay = Get-RelaunchDelay $delay ([timespan]::Zero); $wait = $delay; $log.Write('launcher', "NodeCG could not start: $($_.Exception.Message); retrying in $delay s.") }
            }
            if ($null -eq $nodecg -and $wait -gt 0) { $wait-- }
            Start-Sleep -Seconds 1
        }
    } finally {
        Stop-NodecgProcess $nodecg $log
        if ($owned) { $mutex.ReleaseMutex() }
        $mutex.Dispose()
    }
}

function Request-NodecgStop {
    New-Item -ItemType Directory -Path (Split-Path -Parent $StopFile) -Force | Out-Null
    [IO.File]::WriteAllText($StopFile, "NodeCG stopped on purpose; -Resume or the next sign-in starts it again.`r`n")
    $mutex = New-Object Threading.Mutex($false, (Get-LauncherMutexName))
    try {
        $free = $false
        try { $free = $mutex.WaitOne(30000) } catch [Threading.AbandonedMutexException] { $free = $true }
        if (-not $free) { throw 'The NodeCG launcher did not stop within 30 seconds.' }
        $mutex.ReleaseMutex()
    } finally { $mutex.Dispose() }
    $entry = Join-Path $NodecgRoot 'start.js'
    $others = @(Get-CimInstance Win32_Process -Filter "Name='node.exe'" | Where-Object { $_.CommandLine -and $_.CommandLine.IndexOf($entry, [StringComparison]::OrdinalIgnoreCase) -ge 0 })
    if ($others.Count) { Write-Warning "NodeCG still runs outside this launcher (pid $(@($others | ForEach-Object { $_.ProcessId }) -join ', ')); Stop-ScheduledTask -TaskName '$TaskName' ends it." }
    Write-Host 'NodeCG stays stopped until -Resume or the next sign-in.'
    return 0
}

function Request-NodecgResume {
    if ([IO.File]::Exists($StopFile)) { [IO.File]::Delete($StopFile) }
    Start-ScheduledTask -TaskName $TaskName
    Write-Host "Started '$TaskName'."
    return 0
}

function Get-TaskArguments($Bound) {
    '--headless'; (ConvertTo-NativeArgument "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe")
    '-NoProfile'; '-ExecutionPolicy'; 'Bypass'; '-File'; (ConvertTo-NativeArgument $LauncherPath)
    foreach ($name in @($Bound.Keys | Sort-Object)) {
        if ($name -in @('Stop', 'Resume', 'Register', 'TaskName') -or [Management.Automation.Cmdlet]::CommonParameters -contains $name) { continue }
        '-' + $name; ConvertTo-NativeArgument ([string]$Bound[$name])
    }
}

function Get-TaskXml([string] $User, [string] $Arguments) {
    $who = [Security.SecurityElement]::Escape($User)
    # A headless console host gives NodeCG no terminal window: Windows Terminal would otherwise show one, where Ctrl+C or a click ends it.
    $hostExe = [Security.SecurityElement]::Escape("$env:SystemRoot\System32\conhost.exe")
    $line = [Security.SecurityElement]::Escape($Arguments)
    $folder = [Security.SecurityElement]::Escape((Split-Path -Parent $LauncherPath))
    @"
<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo><Description>Keeps NodeCG with EclipseGraphics and party-visuals running (party-visuals Start-PartyVisualsNodecg.ps1)</Description></RegistrationInfo>
  <Triggers>
    <LogonTrigger><UserId>$who</UserId></LogonTrigger>
  </Triggers>
  <Principals><Principal id="Author"><UserId>$who</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>5</Priority>
    <RestartOnFailure><Interval>PT1M</Interval><Count>999</Count></RestartOnFailure>
  </Settings>
  <Actions Context="Author"><Exec><Command>$hostExe</Command><Arguments>$line</Arguments><WorkingDirectory>$folder</WorkingDirectory></Exec></Actions>
</Task>
"@
}

function Register-NodecgTask($Bound) {
    $user = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    Register-ScheduledTask -TaskName $TaskName -Xml (Get-TaskXml $user ((Get-TaskArguments $Bound) -join ' ')) -Force | Out-Null
    Write-Host "Registered '$TaskName'. Stop and start it once to run this launcher now."
    return 0
}

function Invoke-NodecgLauncher {
    if (@(@($Stop, $Resume, $Register) | Where-Object { $_ }).Count -gt 1) { throw 'Use only one of -Stop, -Resume and -Register.' }
    if ($Stop) { return Request-NodecgStop }
    if ($Resume) { return Request-NodecgResume }
    if ($Register) { return Register-NodecgTask $LaunchParameters }
    return Start-NodecgSupervisor
}

if ($MyInvocation.InvocationName -eq '.') { return }
exit (Invoke-NodecgLauncher)
