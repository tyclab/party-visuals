#Requires -Version 5.1
[CmdletBinding()]
param(
    [string] $ChromeExe = "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
    [string] $NodeExe = "$env:ProgramFiles\nodejs\node.exe",
    [string] $BrowserProfile = "$env:LOCALAPPDATA\PartyVisuals\browser-profile",
    [string] $HelperPath = "$env:LOCALAPPDATA\PartyVisuals\browser-visuals\cli.js",
    [string] $VisualizerUrl = 'http://10.27.2.42:8095/#/now-playing?player=02%3A01%3Abb%3A12%3A81%3A49&frameless=1',
    [string] $ControlsUrl = 'http://127.0.0.1:9090/bundles/party-visuals/api/state',
    [ValidatePattern('^[A-Z0-9]{7}$')]
    [string] $MonitorId = 'SAM7140',
    [switch] $Curtain,
    [string] $NodecgConfig = "$env:LOCALAPPDATA\PartyVisuals\EclipseGraphics\cfg\party-visuals.json",
    [ValidateRange(1, 65535)]
    [int] $Fixture = 53
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

function Initialize-BrowserNative {
    if ('PartyBrowserNative' -as [type]) { return }
    Add-Type @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
[StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)]
public struct PartyDisplayDevice {
 public int cb;
 [MarshalAs(UnmanagedType.ByValTStr, SizeConst=32)] public string DeviceName;
 [MarshalAs(UnmanagedType.ByValTStr, SizeConst=128)] public string DeviceString;
 public uint StateFlags;
 [MarshalAs(UnmanagedType.ByValTStr, SizeConst=128)] public string DeviceID;
 [MarshalAs(UnmanagedType.ByValTStr, SizeConst=128)] public string DeviceKey;
}
[StructLayout(LayoutKind.Sequential)]
public struct PartyRect { public int Left, Top, Right, Bottom; }
[StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)]
public struct PartyMonitorInfo {
 public int cb;
 public PartyRect Monitor, Work;
 public uint Flags;
 [MarshalAs(UnmanagedType.ByValTStr, SizeConst=32)] public string DeviceName;
}
public class PartyDesktopDisplay {
 public string Name, HardwareId;
 public bool Active;
 public int X, Y, Width, Height;
}
public static class PartyBrowserNative {
 delegate bool WindowVisitor(IntPtr window, IntPtr param);
 delegate bool MonitorVisitor(IntPtr monitor, IntPtr dc, ref PartyRect rect, IntPtr param);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern bool EnumDisplayDevices(string device, uint index, ref PartyDisplayDevice item, uint flags);
 [DllImport("user32.dll")] static extern bool EnumDisplayMonitors(IntPtr dc, IntPtr clip, MonitorVisitor callback, IntPtr param);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern bool GetMonitorInfo(IntPtr monitor, ref PartyMonitorInfo info);
 [DllImport("user32.dll")] static extern bool EnumWindows(WindowVisitor callback, IntPtr param);
 [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr window, out uint pid);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] static extern int GetClassName(IntPtr window, StringBuilder name, int capacity);
 [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr window, IntPtr after, int x, int y, int width, int height, uint flags);
 [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr window, int command);
 [DllImport("user32.dll")] static extern bool SetProcessDpiAwarenessContext(IntPtr context);
 [DllImport("user32.dll")] static extern bool SetProcessDPIAware();
 [DllImport("shell32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern IntPtr CommandLineToArgvW(string command, out int count);
 [DllImport("kernel32.dll")] static extern IntPtr LocalFree(IntPtr memory);
 public static void ConfigureDpi() {
  try { SetProcessDpiAwarenessContext(new IntPtr(-4)); }
  catch (EntryPointNotFoundException) { SetProcessDPIAware(); }
 }
 public static PartyDesktopDisplay[] Displays() {
  var displays = new List<PartyDesktopDisplay>();
  EnumDisplayMonitors(IntPtr.Zero, IntPtr.Zero, delegate(IntPtr monitor, IntPtr dc, ref PartyRect rect, IntPtr param) {
   var info = new PartyMonitorInfo(); info.cb = Marshal.SizeOf(info);
   if (GetMonitorInfo(monitor, ref info)) {
    // An attached but inactive monitor can be listed first on every output; use the active child.
    for (uint index = 0; ; index++) {
     var device = new PartyDisplayDevice(); device.cb = Marshal.SizeOf(device);
     if (!EnumDisplayDevices(info.DeviceName, index, ref device, 0)) break;
     if ((device.StateFlags & 1) == 0) continue;
     displays.Add(new PartyDesktopDisplay {
      Name = info.DeviceName, HardwareId = device.DeviceID, Active = true,
      X = info.Monitor.Left, Y = info.Monitor.Top,
      Width = info.Monitor.Right - info.Monitor.Left, Height = info.Monitor.Bottom - info.Monitor.Top
     });
     break;
    }
   }
   return true;
  }, IntPtr.Zero);
  return displays.ToArray();
 }
 public static IntPtr[] Windows(int processId) {
  var windows = new List<IntPtr>();
  EnumWindows(delegate(IntPtr window, IntPtr param) {
   uint pid; GetWindowThreadProcessId(window, out pid);
   if (pid == processId) {
    var name = new StringBuilder(256); GetClassName(window, name, name.Capacity);
    if (name.ToString() == "Chrome_WidgetWin_1") windows.Add(window);
   }
   return true;
  }, IntPtr.Zero);
  return windows.ToArray();
 }
 public static string[] Arguments(string command) {
  int count; var memory = CommandLineToArgvW(command, out count);
  if (memory == IntPtr.Zero) return new string[0];
  try {
   var args = new string[count];
   for (int i = 0; i < count; i++) args[i] = Marshal.PtrToStringUni(Marshal.ReadIntPtr(memory, i * IntPtr.Size));
   return args;
  } finally { LocalFree(memory); }
 }
}
'@
    [PartyBrowserNative]::ConfigureDpi()
}

function Get-DesktopDisplays {
    # Enumerate anew so hotplug detection does not depend on a GUI message loop.
    [PartyBrowserNative]::Displays()
}

function Get-VisualsDisplay {
    $matches = @(Get-DesktopDisplays | Where-Object { $_.Active -and $_.HardwareId -match ('^MONITOR\\' + [regex]::Escape($MonitorId) + '\\') -and $_.Width -gt 0 -and $_.Height -gt 0 })
    if ($matches.Count -gt 1) { throw 'More than one matching TV is active; an unambiguous display is required.' }
    if ($matches.Count -eq 1) { return $matches[0] }
    return $null
}

function ConvertTo-NativeArgument([string] $Value) {
    if ($Value -match '[\r\n\x00]') { throw 'Process arguments cannot contain line breaks or NUL.' }
    return '"' + [regex]::Replace([regex]::Replace($Value, '(\\*)"', '$1$1\"'), '(\\+)$', '$1$1') + '"'
}

function Get-BrowserArguments($Display) {
    @('--user-data-dir=' + $BrowserProfile)
    '--remote-debugging-address=127.0.0.1'
    '--remote-debugging-port=0'
    '--no-first-run'
    '--no-default-browser-check'
    '--start-fullscreen'
    '--app=' + $VisualizerUrl
    '--window-position=' + $Display.X + ',' + $Display.Y
    '--window-size=' + $Display.Width + ',' + $Display.Height
}

function Get-HelperArguments {
    $HelperPath
    '--browser-profile'; $BrowserProfile
    '--visualizer-url'; $VisualizerUrl
    '--controls-url'; $ControlsUrl
    '--parent-pid'; [string]$PID
    if ($Curtain) {
        '--curtain'; '--nodecg-config'; $NodecgConfig
        '--fixture'; [string]$Fixture
        '--width'; '68'; '--height'; '42'; '--fps'; '10'; '--fit'; 'cover'
    }
}

function Get-ProfileBrowser {
    $session = (Get-Process -Id $PID).SessionId
    $profile = [IO.Path]::GetFullPath($BrowserProfile).TrimEnd('\')
    $found = @(foreach ($candidate in Get-CimInstance Win32_Process -Filter "Name='chrome.exe'") {
        if ($candidate.SessionId -ne $session -or -not $candidate.CommandLine) { continue }
        $args = [PartyBrowserNative]::Arguments($candidate.CommandLine)
        $dirs = @($args | Where-Object { $_.StartsWith('--user-data-dir=', [StringComparison]::OrdinalIgnoreCase) })
        if ($dirs.Count -ne 1) { continue }
        $directory = [IO.Path]::GetFullPath($dirs[0].Substring(16)).TrimEnd('\')
        if ($directory -ine $profile -or @($args | Where-Object { $_ -like '--type=*' }).Count -gt 0) { continue }
        if ($args -notcontains '--remote-debugging-port=0' -or $args -notcontains '--remote-debugging-address=127.0.0.1' -or $args -notcontains '--start-fullscreen' -or $args -notcontains ('--app=' + $VisualizerUrl)) {
            throw 'The dedicated browser profile is already open with different launch options. Close that profile before starting this task.'
        }
        Get-Process -Id $candidate.ProcessId -ErrorAction SilentlyContinue
    })
    if ($found.Count -gt 1) { throw 'Multiple browsers own the dedicated profile; refusing another launch.' }
    if ($found.Count -eq 1) { return $found[0] }
    return $null
}

function Set-BrowserDisplay([int] $ProcessId, $Display) {
    $windows = @([PartyBrowserNative]::Windows($ProcessId))
    foreach ($window in $windows) {
        if ($null -eq $Display) {
            [PartyBrowserNative]::ShowWindow($window, 0) | Out-Null
        } else {
            if (-not [PartyBrowserNative]::SetWindowPos($window, [IntPtr]::Zero, $Display.X, $Display.Y, $Display.Width, $Display.Height, 0x0014)) { throw 'Could not place the dedicated browser on the TV.' }
            [PartyBrowserNative]::ShowWindow($window, 8) | Out-Null
        }
    }
    return ($windows.Count -gt 0)
}

function Start-BrowserHelper {
    $info = New-Object Diagnostics.ProcessStartInfo
    $info.FileName = $NodeExe
    $info.WorkingDirectory = Split-Path -Parent $HelperPath
    $info.Arguments = ((Get-HelperArguments | ForEach-Object { ConvertTo-NativeArgument $_ }) -join ' ')
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardInput = $true
    $process = New-Object Diagnostics.Process
    $process.StartInfo = $info
    if (-not $process.Start()) { throw 'Could not start the local browser helper.' }
    return $process
}

function Stop-BrowserHelper($Process) {
    if ($null -eq $Process) { return }
    try {
        if (-not $Process.HasExited) {
            try { $Process.StandardInput.WriteLine('stop'); $Process.StandardInput.Flush() } catch { }
            if (-not $Process.WaitForExit(5000)) {
                $Process.Kill()
                if (-not $Process.WaitForExit(2000)) { throw 'The previous browser helper did not stop; refusing another instance.' }
            }
        }
    } finally { $Process.Dispose() }
}

function Start-VisualsBrowser {
    foreach ($path in @($ChromeExe, $NodeExe, $HelperPath)) {
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Required executable or helper is missing: $path" }
    }
    if ($Curtain -and -not (Test-Path -LiteralPath $NodecgConfig -PathType Leaf)) { throw 'The curtain configuration is missing.' }
    $profile = [IO.Path]::GetFullPath($BrowserProfile).TrimEnd('\')
    $normalProfile = [IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'Google\Chrome\User Data')).TrimEnd('\')
    if ($profile -ieq $normalProfile -or $profile.StartsWith($normalProfile + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'A separate PartyVisuals browser profile is required; the normal Chrome profile is not supported.' }
    $visualizer = [uri]$VisualizerUrl
    $controls = [uri]$ControlsUrl
    if ($visualizer.Scheme -notin @('http', 'https') -or $visualizer.UserInfo -or $controls.Scheme -ne 'http' -or $controls.Host -ne '127.0.0.1' -or $controls.UserInfo) { throw 'Use a plain visualizer URL and loopback HTTP controls URL, without credentials.' }
    Initialize-BrowserNative
    $mutex = New-Object Threading.Mutex($false, 'Local\PartyVisuals-Browser-Launch')
    $owned = $false
    $helper = $null
    $browser = $null
    $lastDisplay = ''
    $lastMessage = ''
    try {
        try { $owned = $mutex.WaitOne(0) } catch [Threading.AbandonedMutexException] { $owned = $true }
        if (-not $owned) { return 0 }
        while ($true) {
            $display = Get-VisualsDisplay
            if ($null -eq $browser -or $browser.HasExited) {
                Stop-BrowserHelper $helper
                $helper = $null
                $browser = Get-ProfileBrowser
                $lastDisplay = ''
            }
            if ($null -eq $display) {
                if ($null -ne $browser) { Set-BrowserDisplay $browser.Id $null | Out-Null }
                Stop-BrowserHelper $helper
                $helper = $null
                $lastDisplay = ''
                if ($lastMessage -ne 'waiting') { Write-Host 'Waiting for the Q90A to be an active Windows desktop display.'; $lastMessage = 'waiting' }
                Start-Sleep -Seconds 2
                continue
            }
            if ($null -eq $browser) {
                New-Item -ItemType Directory -Path $BrowserProfile -Force | Out-Null
                $portFile = Join-Path $BrowserProfile 'DevToolsActivePort'
                if (Test-Path -LiteralPath $portFile) { Remove-Item -LiteralPath $portFile }
                $args = ((Get-BrowserArguments $display | ForEach-Object { ConvertTo-NativeArgument $_ }) -join ' ')
                $browser = Start-Process -FilePath $ChromeExe -WorkingDirectory (Split-Path -Parent $ChromeExe) -ArgumentList $args -PassThru
                Start-Sleep -Seconds 2
                continue
            }
            $key = '{0}:{1},{2},{3},{4}' -f $display.Name, $display.X, $display.Y, $display.Width, $display.Height
            if ($lastDisplay -ne $key) {
                if (-not (Set-BrowserDisplay $browser.Id $display)) { Start-Sleep -Seconds 1; continue }
                $lastDisplay = $key
            }
            if ($null -ne $helper -and $helper.HasExited) {
                Stop-BrowserHelper $helper
                $helper = $null
                Start-Sleep -Seconds 3
            }
            if ($null -eq $helper) { $helper = Start-BrowserHelper }
            if ($lastMessage -ne 'running') { Write-Host 'The dedicated browser is on the Q90A; the local visuals helper is running.'; $lastMessage = 'running' }
            Start-Sleep -Seconds 1
        }
    } finally {
        Stop-BrowserHelper $helper
        if ($owned) { $mutex.ReleaseMutex() }
        $mutex.Dispose()
    }
}

if ($MyInvocation.InvocationName -eq '.') { return }
exit (Start-VisualsBrowser)
