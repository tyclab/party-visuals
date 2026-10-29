#Requires -Version 5.1
[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [ValidateSet('Stage','Cutover','Rollback')][string] $Action = 'Stage',
    [string] $Root = "$env:LOCALAPPDATA\PartyVisuals",
    [string] $ControllerExe,
    [ValidatePattern('^[a-fA-F0-9]{64}$|^$')][string] $ExpectedSha256 = '',
    [string] $ConfigPath,
    [switch] $ImportLegacy
)
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'
$ControllerTask = 'PartyVisuals Controller'
$LegacyTasks = @('PartyVisuals Browser','PartyVisuals NodeCG')
$StateRoot = Join-Path $Root 'controller'
$JournalPath = Join-Path $StateRoot 'migration.json'

function Save-ControllerJson([string] $Path, $Value) {
    $temp = $Path + '.new'
    [IO.File]::WriteAllText($temp, ($Value | ConvertTo-Json -Depth 12), (New-Object Text.UTF8Encoding($false)))
    if ([IO.File]::Exists($Path)) {
        $backup = $Path + '.previous'
        [IO.File]::Replace($temp, $Path, $backup)
        [IO.File]::Delete($backup)
    } else { [IO.File]::Move($temp, $Path) }
}
function Protect-ControllerFolder([string] $Path) {
    $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $acl = New-Object Security.AccessControl.DirectorySecurity
    $acl.SetSecurityDescriptorSddlForm("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;$sid)", [Security.AccessControl.AccessControlSections]::Access)
    $folder = New-Object IO.DirectoryInfo($Path)
    if ($PSVersionTable.PSEdition -eq 'Core') { [IO.FileSystemAclExtensions]::SetAccessControl($folder, $acl) } else { $folder.SetAccessControl($acl) }
}
function Assert-OwnedTask($Task, [string] $ExpectedFile) {
    if ($null -eq $Task) { return }
    $actions = @($Task.Actions)
    $owned = $false
    if ($actions.Count -eq 1) {
        if ($ExpectedFile.EndsWith('\')) {
            $actual = [IO.Path]::GetFullPath($actions[0].Execute)
            $owned = $actual.StartsWith([IO.Path]::GetFullPath($ExpectedFile), [StringComparison]::OrdinalIgnoreCase) -and [IO.Path]::GetFileName($actual) -eq 'party-visuals.exe'
        } else {
            Initialize-ControllerArguments
            $args = [PartyBrowserNative]::Arguments('launcher ' + $actions[0].Arguments)
            $exe = [IO.Path]::GetFileName($actions[0].Execute)
            $isPowerShell = $exe -ieq 'powershell.exe'
            $first = 1
            if ($exe -ieq 'conhost.exe' -and $args.Count -gt 3) {
                $isPowerShell = [IO.Path]::GetFullPath($actions[0].Execute) -ieq (Join-Path $env:SystemRoot 'System32\conhost.exe') -and $args[1] -eq '--headless' -and [IO.Path]::GetFullPath($args[2]) -ieq (Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe')
                $first = 3
            }
            for ($i = $first; $i -lt $args.Count - 1; $i++) {
                if ($args[$i] -ieq '-File') {
                    $owned = $isPowerShell -and [IO.Path]::GetFullPath($args[$i + 1]) -ieq [IO.Path]::GetFullPath($ExpectedFile)
                    break
                }
                if ($args[$i] -ieq '-Command' -or $args[$i] -ieq '-EncodedCommand') { break }
            }
        }
    }
    if (-not $owned) { throw "Task '$($Task.TaskName)' is not the expected owned launcher; refusing takeover." }
}
function Initialize-ControllerArguments {
    if (-not ('PartyBrowserNative' -as [type])) {
        . (Join-Path $PSScriptRoot 'Start-PartyVisualsBrowser.ps1')
        Initialize-BrowserNative
    }
}
function Get-StopArguments($Task) {
    Initialize-ControllerArguments
    $args = [PartyBrowserNative]::Arguments('launcher ' + $Task.Actions[0].Arguments)
    $allowed = @('BrowserProfile','NodecgRoot','StopFile','TaskName')
    for ($i = 1; $i -lt $args.Count; $i++) {
        if ($args[$i].TrimStart('-') -notin $allowed) { continue }
        $args[$i]
        if ($i + 1 -ge $args.Count) { throw 'Incomplete legacy stop argument.' }
        $i++; $args[$i]
    }
}
function Get-LegacyConfiguration {
    $browserFile = Join-Path $Root 'startup\Start-PartyVisualsBrowser.ps1'
    $nodeFile = Join-Path $Root 'startup\Start-PartyVisualsNodecg.ps1'
    if (-not (Test-Path -LiteralPath $browserFile) -or -not (Test-Path -LiteralPath $nodeFile)) { throw 'Legacy launchers not found; supply a reviewed -ConfigPath.' }
    . $nodeFile
    $savedNodeRoot = $NodecgRoot
    . $browserFile
    Initialize-BrowserNative
    $nodeTask = Get-ScheduledTask -TaskName 'PartyVisuals NodeCG' -ErrorAction Stop
    Assert-OwnedTask $nodeTask $nodeFile
    $nodeArgs = [PartyBrowserNative]::Arguments('launcher ' + $nodeTask.Actions[0].Arguments)
    for ($i = 1; $i -lt $nodeArgs.Count - 1; $i++) { if ($nodeArgs[$i] -ieq '-NodecgRoot') { $savedNodeRoot = $nodeArgs[$i + 1] } }
    $task = Get-ScheduledTask -TaskName 'PartyVisuals Browser' -ErrorAction Stop
    Assert-OwnedTask $task $browserFile
    $args = [PartyBrowserNative]::Arguments('powershell.exe ' + $task.Actions[0].Arguments)
    $allowed = @('ChromeExe','NodeExe','BrowserProfile','HelperPath','VisualizerUrl','ControlsUrl','TelemetryFile','NodecgConfig','Fixture','Fps','Panels','Curtain','Room')
    for ($i = 1; $i -lt $args.Count; $i++) {
        $name = $args[$i].TrimStart('-')
        if ($name -notin $allowed) { continue }
        if ($name -in @('Curtain','Room')) { Set-Variable $name $true } else {
            if ($i + 1 -ge $args.Count) { throw 'Incomplete legacy launcher argument.' }
            $i++; Set-Variable $name $args[$i]
        }
    }
    @{
        version = 1; workerScript = ''; nodeExe = $NodeExe; chromeExe = $ChromeExe
        nodecgRoot = $savedNodeRoot; browserProfile = $BrowserProfile; helperPath = $HelperPath
        visualizerUrl = $VisualizerUrl; controlsUrl = $ControlsUrl; nodecgConfig = $NodecgConfig
        telemetryFile = $TelemetryFile; statusFile = (Join-Path $Root 'logs\controller-helper.json')
        logFile = (Join-Path $Root 'logs\nodecg.log'); curtain = [bool]$Curtain; room = [bool]$Room
        panels = [string]$Panels; fixture = [int]$Fixture; fps = [int]$Fps
    }
}
function Test-ControllerBinary([string] $Exe, [string] $CheckRoot = $StateRoot) {
    & $Exe --root $CheckRoot check
    if ($LASTEXITCODE -ne 0) { throw 'Staged controller configuration check failed; existing tasks remain unchanged.' }
}
function Enter-ControllerOwnership {
    $guard = [IO.File]::Open((Join-Path $StateRoot 'owner.lock'), 'OpenOrCreate', 'ReadWrite', 'ReadWrite')
    try { $guard.Lock(0,1) } catch { $guard.Dispose(); throw 'A controller is running or its lock is inaccessible; stop it before changing owners.' }
    return $guard
}
function Stage-Controller {
    if (-not $ControllerExe -or -not $ExpectedSha256) { throw 'Stage requires -ControllerExe and its reviewed -ExpectedSha256.' }
    $hash = (Get-FileHash -LiteralPath $ControllerExe -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($hash -ne $ExpectedSha256.ToLowerInvariant()) { throw 'Controller checksum mismatch.' }
    $bin = Join-Path $StateRoot ('releases\' + $hash.Substring(0,12))
    New-Item -ItemType Directory -Path $bin -Force | Out-Null
    $exe = Join-Path $bin 'party-visuals.exe'
    Copy-Item -LiteralPath $ControllerExe -Destination $exe
    foreach ($name in @('Run-PartyVisualsSession.ps1','Start-PartyVisualsBrowser.ps1','Start-PartyVisualsNodecg.ps1')) { Copy-Item -LiteralPath (Join-Path $PSScriptRoot $name) -Destination (Join-Path $bin $name) -Force }
    $targetConfig = Join-Path $StateRoot 'config.json'
    $config = if ($ConfigPath) { Get-Content -LiteralPath $ConfigPath -Raw | ConvertFrom-Json } elseif ($ImportLegacy) { Get-LegacyConfiguration } elseif (Test-Path -LiteralPath $targetConfig) { Get-Content -LiteralPath $targetConfig -Raw | ConvertFrom-Json } else { throw 'Supply -ConfigPath or -ImportLegacy for the first installation.' }
    $config.workerScript = Join-Path $bin 'Run-PartyVisualsSession.ps1'
    if (Test-Path -LiteralPath $targetConfig) { Copy-Item -LiteralPath $targetConfig -Destination (Join-Path $bin 'previous-config.json') }
    Save-ControllerJson (Join-Path $bin 'config.json') $config
    $intent = Join-Path $StateRoot 'intent.json'
    if (-not (Test-Path -LiteralPath $intent)) { Save-ControllerJson $intent @{ version = 1; mode = 'manual'; manualOff = $true; requested = $false } }
    Copy-Item -LiteralPath $intent -Destination (Join-Path $bin 'intent.json') -Force
    Test-ControllerBinary $exe $bin
    $previous = if (Test-Path -LiteralPath $JournalPath) { Get-Content -LiteralPath $JournalPath -Raw | ConvertFrom-Json } else { $null }
    $prior = @()
    if ($null -ne $previous -and $previous.version -eq 1 -and $previous.phase -ne 'rolled-back-off') {
        $prior = @($previous.tasks)
    } else {
    foreach ($name in $LegacyTasks) {
        $task = Get-ScheduledTask -TaskName $name -ErrorAction SilentlyContinue
        if ($null -eq $task) { continue }
        $scriptName = if ($name -eq 'PartyVisuals Browser') { 'Start-PartyVisualsBrowser.ps1' } else { 'Start-PartyVisualsNodecg.ps1' }
        Assert-OwnedTask $task (Join-Path $Root ('startup\' + $scriptName))
        $file = Join-Path $bin ($name.Replace(' ','-') + '.xml')
        [IO.File]::WriteAllText($file, (Export-ScheduledTask -TaskName $name), (New-Object Text.UnicodeEncoding))
        $prior += @{ name = $name; enabled = [bool]$task.Settings.Enabled; xml = $file; script = (Join-Path $Root ('startup\' + $scriptName)); stopArguments = @(Get-StopArguments $task) }
    }
    }
    $journal = @{ version = 1; phase = 'staged'; exe = $exe; config = (Join-Path $bin 'config.json'); sha256 = $hash; tasks = @($prior) }
    Save-ControllerJson $JournalPath $journal
    Write-Host 'Controller staged and checked. Existing tasks and running visuals are unchanged.'
}
function Register-ControllerTask([string] $Exe) {
    $existing = Get-ScheduledTask -TaskName $ControllerTask -ErrorAction SilentlyContinue
    if ($null -ne $existing) { Assert-OwnedTask $existing (Join-Path $StateRoot 'releases\') }
    $user = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    $action = New-ScheduledTaskAction -Execute $Exe -Argument ('--root "' + $StateRoot + '" app') -WorkingDirectory (Split-Path -Parent $Exe)
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $user
    $settings = New-ScheduledTaskSettingsSet -MultipleInstances IgnoreNew -ExecutionTimeLimit ([timespan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -RestartCount 999 -RestartInterval ([timespan]::FromMinutes(1))
    Register-ScheduledTask -TaskName $ControllerTask -Action $action -Trigger $trigger -Settings $settings -User $user -RunLevel Limited -Force | Out-Null
}
function Wait-ControllerReady([string] $Exe) {
    $until = [datetime]::UtcNow.AddSeconds(30)
    do {
        $priorErrorPreference = $ErrorActionPreference
        try { $ErrorActionPreference = 'Continue'; $result = & $Exe --root $StateRoot status 2>$null; $resultCode = $LASTEXITCODE } finally { $ErrorActionPreference = $priorErrorPreference }
        if ($resultCode -eq 0) {
            $status = ($result -join [Environment]::NewLine) | ConvertFrom-Json
            if ($status.state.phase -eq 'off' -and $status.state.intent.manualOff) { return }
        }
        Start-Sleep -Milliseconds 250
    } while ([datetime]::UtcNow -lt $until)
    throw 'Controller did not become reachable and Off; legacy owners remain disabled. Retry Cutover after resolving the controller, or Rollback.'
}
function Cutover-Controller($Journal) {
    Test-ControllerBinary $Journal.exe (Split-Path -Parent $Journal.config)
    $guard = Enter-ControllerOwnership
    try {
    Save-ControllerJson (Join-Path $StateRoot 'config.json') (Get-Content -LiteralPath $Journal.config -Raw | ConvertFrom-Json)
    foreach ($record in $Journal.tasks) {
        $task = Get-ScheduledTask -TaskName $record.name -ErrorAction SilentlyContinue
        Assert-OwnedTask $task $record.script
        if ($null -ne $task) { Disable-ScheduledTask -TaskName $record.name | Out-Null }
    }
    $Journal.phase = 'legacy-disabled'; Save-ControllerJson $JournalPath $Journal
    foreach ($record in $Journal.tasks) {
        $stopArguments = @($record.stopArguments)
        & "$env:SystemRoot\System32\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $record.script @stopArguments -Stop
        if ($LASTEXITCODE -ne 0) { throw 'Legacy graceful Stop failed; new owner has not been started.' }
        Stop-ScheduledTask -TaskName $record.name -ErrorAction SilentlyContinue
    }
    $Journal.phase = 'legacy-stopped'; Save-ControllerJson $JournalPath $Journal
    Register-ControllerTask $Journal.exe
    $Journal.phase = 'registered'; Save-ControllerJson $JournalPath $Journal
    } finally { $guard.Dispose() }
    Start-ScheduledTask -TaskName $ControllerTask
    Wait-ControllerReady $Journal.exe
    $Journal.phase = 'active'; Save-ControllerJson $JournalPath $Journal
    Write-Host 'Controller activated. Start visuals explicitly from the tray or CLI; rig controls were not changed.'
}
function Rollback-Controller($Journal) {
    $task = Get-ScheduledTask -TaskName $ControllerTask -ErrorAction SilentlyContinue
    Assert-OwnedTask $task (Join-Path $StateRoot 'releases\')
    if ($null -ne $task) {
        Disable-ScheduledTask -TaskName $ControllerTask | Out-Null
        & $Journal.exe --root $StateRoot quit 2>$null
        $until = [datetime]::UtcNow.AddSeconds(60)
        while ((Get-ScheduledTask -TaskName $ControllerTask).State -eq 'Running' -and [datetime]::UtcNow -lt $until) { Start-Sleep -Milliseconds 200 }
        if ((Get-ScheduledTask -TaskName $ControllerTask).State -eq 'Running') { throw 'New owner has not stopped; prior supervisors remain disabled.' }
    }
    $guard = Enter-ControllerOwnership
    try {
    $intentPath = Join-Path $StateRoot 'intent.json'
    $intent = Get-Content -LiteralPath $intentPath -Raw | ConvertFrom-Json
    $intent.manualOff = $true; $intent.requested = $false; Save-ControllerJson $intentPath $intent
    foreach ($record in $Journal.tasks) {
        Register-ScheduledTask -TaskName $record.name -Xml ([IO.File]::ReadAllText($record.xml)) -Force | Out-Null
        Disable-ScheduledTask -TaskName $record.name | Out-Null
    }
    $Journal.phase = 'rolled-back-off'; Save-ControllerJson $JournalPath $Journal
    } finally { $guard.Dispose() }
    Write-Host 'Prior task definitions restored disabled, preserving Manual Off. Explicitly reconcile autostart before using legacy launchers.'
}
function Invoke-ControllerInstall {
    if (-not $PSCmdlet.ShouldProcess($Root, "$Action PartyVisuals controller")) { return }
    if (Test-Path -LiteralPath $StateRoot) { if ((Get-Item -LiteralPath $StateRoot).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Controller root cannot be a link.' } }
    New-Item -ItemType Directory -Path $StateRoot -Force | Out-Null
    Protect-ControllerFolder $StateRoot
    $lock = [IO.File]::Open((Join-Path $StateRoot 'install.lock'), 'OpenOrCreate', 'ReadWrite', 'ReadWrite')
    try {
        $lock.Lock(0,1)
        if ($Action -eq 'Stage') { Stage-Controller; return }
        $journal = Get-Content -LiteralPath $JournalPath -Raw | ConvertFrom-Json
        if ($journal.version -ne 1) { throw 'Unsupported migration journal.' }
        if ($Action -eq 'Cutover') { Cutover-Controller $journal } else { Rollback-Controller $journal }
    } finally { $lock.Dispose() }
}
if ($MyInvocation.InvocationName -eq '.') { return }
Invoke-ControllerInstall
