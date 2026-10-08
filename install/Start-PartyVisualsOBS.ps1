#Requires -Version 5.1
[CmdletBinding()]
param(
    [string] $ObsExe = "$env:ProgramFiles\obs-studio\bin\64bit\obs64.exe",
    [ValidatePattern('^[^"\r\n]+$')]
    [string] $Collection = 'Party Visuals',
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^[^"\r\n]+$')]
    [string] $Profile,
    [ValidateRange(1, 3600)]
    [int] $WaitSeconds = 300
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

function Test-VisualsReady {
    foreach ($graphic in @('wash', 'bar')) {
        try {
            $r = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:9090/bundles/party-visuals/graphics/$graphic.html" -TimeoutSec 3
            if ($r.StatusCode -ne 200) { return $false }
        } catch { return $false }
    }
    return $true
}

function Start-VisualsOBS {
    if (-not (Test-Path -LiteralPath $ObsExe -PathType Leaf)) { throw "OBS executable is missing: $ObsExe" }
    $mutex = New-Object System.Threading.Mutex($false, 'Local\PartyVisuals-OBS-Launch')
    $owned = $false
    try {
        try { $owned = $mutex.WaitOne(0) } catch [System.Threading.AbandonedMutexException] { $owned = $true }
        if (-not $owned) { return 0 }
        $session = (Get-Process -Id $PID).SessionId
        if (Get-Process -Name obs64 -ErrorAction SilentlyContinue | Where-Object { $_.SessionId -eq $session }) { return 0 }
        $deadline = [DateTime]::UtcNow.AddSeconds($WaitSeconds)
        while (-not (Test-VisualsReady)) {
            if ([DateTime]::UtcNow -ge $deadline) { throw 'NodeCG graphics are unavailable; Task Scheduler can retry this launch.' }
            Start-Sleep -Seconds 2
        }
        # Keep the task alive so a failed OBS process can trigger task recovery.
        $args = '--collection "{0}" --profile "{1}" --scene "{0}" --minimize-to-tray' -f $Collection, $Profile
        $process = Start-Process -FilePath $ObsExe -WorkingDirectory (Split-Path -Parent $ObsExe) -ArgumentList $args -PassThru
        $process.WaitForExit()
        return $process.ExitCode
    } finally {
        if ($owned) { $mutex.ReleaseMutex() }
        $mutex.Dispose()
    }
}

if ($MyInvocation.InvocationName -eq '.') { return }
exit (Start-VisualsOBS)
