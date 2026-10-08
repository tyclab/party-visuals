#Requires -Version 5.1
<#
Checks install\Install-PartyVisuals.ps1 on Windows, in a folder under %TEMP%
that it removes again: the token file, the configuration merges, the install
folder checks and -Uninstall. It installs nothing, registers no scheduled task
and needs neither Node.js nor git. Windows PowerShell 5.1 or PowerShell 7:

  powershell -NoProfile -ExecutionPolicy Bypass -File test\install.test.ps1

The -Uninstall runs expect nothing listening on port 9090 (NodeCG stopped).
#>
Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

if ($env:OS -ne 'Windows_NT') {
    Write-Host 'install.test.ps1: Windows only, skipped.'
    exit 0
}

$installer = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..\install\Install-PartyVisuals.ps1')).ProviderPath
$work = Join-Path ([System.IO.Path]::GetTempPath()) ('party-visuals-test-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $work | Out-Null

$script:passed = 0
$script:failed = 0

function Check([string] $Name, [bool] $Ok, [string] $Detail = '') {
    if ($Ok) {
        $script:passed++
        Write-Host "ok    $Name"
    } else {
        $script:failed++
        Write-Host "FAIL  $Name" -ForegroundColor Red
        if ($Detail) { Write-Host $Detail }
    }
}

function Test-Throws([scriptblock] $Block) {
    try { & $Block | Out-Null } catch { return $true }
    return $false
}

function New-Secure([string] $Text) {
    $s = New-Object System.Security.SecureString
    foreach ($c in $Text.ToCharArray()) { $s.AppendChar($c) }
    $s.MakeReadOnly()
    return $s
}

function Read-Text([string] $Path) { return [System.IO.File]::ReadAllText($Path) }

function Invoke-Installer([string[]] $Arguments) {
    # Its own process of the same PowerShell, as the operator runs it.
    $exe = (Get-Process -Id $PID).Path
    $saved = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $out = & $exe -NoProfile -ExecutionPolicy Bypass -File $installer @Arguments 2>&1 | Out-String
        $code = $LASTEXITCODE
    } finally { $ErrorActionPreference = $saved }
    return New-Object PSObject -Property @{ ExitCode = $code; Output = $out }
}

function New-Marked([string] $Dir) {
    New-Item -ItemType Directory -Path $Dir | Out-Null
    [System.IO.File]::WriteAllText((Join-Path $Dir 'party-visuals-install.json'), "{}`n")
}

try {
    # ------------------------------------------------------------ the functions

    # Dot-sourced, the installer defines its functions and stops; -WhatIf in
    # case it ever did not.
    . $installer -Root (Join-Path $work 'unused') -WhatIf
    Check 'dot-sourcing the installer runs no install' (-not (Test-Path -LiteralPath (Join-Path $work 'unused')))

    Check 'a drive root is refused as -Root' (Test-Throws { Resolve-InstallRoot ([System.IO.Path]::GetPathRoot($work)) })
    Check 'a share root is refused as -Root' (Test-Throws { Resolve-InstallRoot '\\server\share\' })
    Check 'a folder is taken without its trailing separator' ((Resolve-InstallRoot "$work\x\") -eq "$work\x")

    $taskOf = {
        param([string] $Dir)
        $action = New-Object PSObject -Property @{ Arguments = "`"$Dir\EclipseGraphics\start.js`""; WorkingDirectory = "$Dir\EclipseGraphics" }
        New-Object PSObject -Property @{ Actions = @($action) }
    }
    Check 'the scheduled task of the folder is recognised' (Test-TaskUsesRoot (& $taskOf 'C:\Users\show\AppData\Local\PartyVisuals') 'C:\Users\show\AppData\Local\PartyVisuals')
    Check 'the task of a folder whose name only starts the same is not' (-not (Test-TaskUsesRoot (& $taskOf 'C:\Users\show\AppData\Local\PartyVisuals2') 'C:\Users\show\AppData\Local\PartyVisuals'))

    Import-Module ScheduledTasks
    $script:registeredTask = $null
    $originalTaskName = $TaskName
    $TaskName = 'PartyVisuals Test ' + [guid]::NewGuid().ToString('N')
    function Register-ScheduledTask {
        param($TaskName, $Description, $Action, $Trigger, $Principal, $Settings, [switch] $Force)
        $script:registeredTask = @{ Name = $TaskName; Action = $Action; Trigger = $Trigger; Principal = $Principal; Settings = $Settings }
    }
    try {
        Register-Autostart -Node 'C:\Program Files\nodejs\node.exe' -StartScript "$work\EclipseGraphics\start.js" -WorkDir "$work\EclipseGraphics" -Confirm:$false
        $t = $script:registeredTask
        Check 'autostart quotes the script and retains the working directory' ($t.Action.Arguments -eq "`"$work\EclipseGraphics\start.js`"" -and $t.Action.WorkingDirectory -eq "$work\EclipseGraphics")
        Check 'autostart uses this user''s interactive limited desktop' ($t.Principal.UserId -eq [System.Security.Principal.WindowsIdentity]::GetCurrent().Name -and $t.Principal.LogonType -eq 'Interactive' -and $t.Principal.RunLevel -eq 'Limited')
        Check 'autostart recovers failures and missed starts without duplicate instances' ($t.Settings.RestartCount -eq 999 -and $t.Settings.RestartInterval -eq 'PT1M' -and $t.Settings.StartWhenAvailable -and $t.Settings.MultipleInstances -eq 'IgnoreNew' -and $t.Settings.ExecutionTimeLimit -eq 'PT0S')
        Check 'autostart test registered no real task' (-not (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue))
    } finally {
        Remove-Item -LiteralPath function:Register-ScheduledTask
        $TaskName = $originalTaskName
    }

    # ------------------------------------------------------------ the token

    $cfg = Join-Path $work 'cfg'
    New-Item -ItemType Directory -Path $cfg | Out-Null
    $tokenFile = Join-Path $cfg 'party-visuals.token'
    $me = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value

    # The file's content when its access is restricted: still empty.
    $restrict = ${function:Set-OwnerOnlyAcl}
    $script:contentAtAcl = $null
    function Set-OwnerOnlyAcl {
        [CmdletBinding(SupportsShouldProcess = $true)]
        param([string] $Path)
        $script:contentAtAcl = [System.IO.File]::ReadAllText($Path)
        & $restrict -Path $Path -Confirm:$false
    }
    $first = 'pv-test-' + [guid]::NewGuid().ToString('N')
    Write-TokenFile -Path $tokenFile -Token (New-Secure $first) -Confirm:$false
    ${function:Set-OwnerOnlyAcl} = $restrict
    Check 'the token file is restricted before the token goes in' ($script:contentAtAcl -eq '')
    Check 'the token is written as entered' ((Read-Text $tokenFile) -eq $first)
    $acl = Get-Acl -LiteralPath $tokenFile
    $rules = @($acl.GetAccessRules($true, $true, [System.Security.Principal.SecurityIdentifier]))
    Check 'the token file inherits no access' $acl.AreAccessRulesProtected
    Check 'the token file has one rule, for this user' ($rules.Count -eq 1 -and $rules[0].IdentityReference.Value -eq $me -and
        $rules[0].AccessControlType -eq [System.Security.AccessControl.AccessControlType]::Allow)

    $second = 'pv-test-' + [guid]::NewGuid().ToString('N')
    Write-TokenFile -Path $tokenFile -Token (New-Secure $second) -Confirm:$false
    Check 'a new token replaces the stored one' ((Read-Text $tokenFile) -eq $second)
    Check 'the replaced token file is still this user''s only' (Test-OwnerOnlyAcl $tokenFile)
    Check 'an empty token is refused and the stored one kept' ((Test-Throws { Write-TokenFile -Path $tokenFile -Token (New-Secure '') -Confirm:$false }) -and (Read-Text $tokenFile) -eq $second)
    Check 'a token with a space is refused and the stored one kept' ((Test-Throws { Write-TokenFile -Path $tokenFile -Token (New-Secure 'two words') -Confirm:$false }) -and (Read-Text $tokenFile) -eq $second)

    # Set-Token asks through Read-Host; here a stand-in answers.
    $third = 'pv-test-' + [guid]::NewGuid().ToString('N')
    $script:answer = New-Secure $third
    $script:asked = 0
    function Read-Host {
        param([switch] $AsSecureString, [string] $Prompt)
        $script:asked++
        return $script:answer
    }
    Set-Token -Path $tokenFile -Confirm:$false
    Check 'a run without -NewToken keeps the stored token and does not ask' ($script:asked -eq 0 -and (Read-Text $tokenFile) -eq $second)

    $transcript = Join-Path $work 'transcript.txt'
    $Error.Clear()
    Start-Transcript -LiteralPath $transcript | Out-Null
    try {
        Set-Token -Path $tokenFile -Replace -Confirm:$false -Verbose
        # A write that fails on the way leaves an error record.
        try { Write-TokenFile -Path (Join-Path $work 'no\such\folder.token') -Token $script:answer -Confirm:$false -Verbose } catch { Write-Host "    (expected) $($_.Exception.Message)" }
    } finally {
        Stop-Transcript | Out-Null
    }
    Remove-Item -LiteralPath function:Read-Host
    $records = @($Error | ForEach-Object { '{0} {1} {2}' -f $_, $_.InvocationInfo.Line, $_.ScriptStackTrace }) -join "`n"
    Check '-NewToken asks once and writes the new token' ($script:asked -eq 1 -and (Read-Text $tokenFile) -eq $third)
    Check 'the token is in neither the transcript nor an error record' ((Read-Text $transcript).IndexOf($third) -lt 0 -and $records.IndexOf($third) -lt 0 -and $Error.Count -gt 0)

    # ------------------------------------------------------------ configuration

    $nodecgJson = Join-Path $cfg 'nodecg.json'
    $nodecgBefore = '{ "host": "0.0.0.0", "port": 9091, "login": { "enabled": false, "sessionSecret": "keep me", ' +
        '"local": { "allowedUsers": [ { "name": "show", "password": "pw" } ] } }, "bundles": { "disabled": [ "other" ] }, ' +
        '"ratio": 0.25, "nothing": null, "flag": true, "empty": [], "obj": {} }'
    [System.IO.File]::WriteAllText($nodecgJson, $nodecgBefore)
    Set-NodecgConfig -Path $nodecgJson -Confirm:$false
    $after = Read-Text $nodecgJson | ConvertFrom-Json
    $others = { param($o) ConvertTo-Json ($o | Select-Object -Property * -ExcludeProperty host, port) -Depth 20 -Compress }
    Check 'nodecg.json: NodeCG on 127.0.0.1:9090' ($after.host -eq '127.0.0.1' -and $after.port -eq 9090)
    Check 'nodecg.json: every other setting kept' ((& $others ($nodecgBefore | ConvertFrom-Json)) -eq (& $others $after))
    Check 'nodecg.json: one-element and empty lists stay lists' ((Read-Text $nodecgJson) -match '"allowedUsers": \[' -and (Read-Text $nodecgJson) -match '"empty": \[\]')
    $bytes = [System.Convert]::ToBase64String([System.IO.File]::ReadAllBytes($nodecgJson))
    Set-NodecgConfig -Path $nodecgJson -Confirm:$false
    Check 'nodecg.json: a second run leaves the file as it is' ($bytes -eq [System.Convert]::ToBase64String([System.IO.File]::ReadAllBytes($nodecgJson)))

    $bundleJson = Join-Path $cfg 'party-visuals.json'
    [System.IO.File]::WriteAllText($bundleJson, '{ "lightshow": { "url": "http://old.example:3000", "tokenFile": "cfg/party-visuals.token", "pollMs": 2000 }, ' +
        '"graphics": { "offsetMs": 120, "maxFps": 60 }, "allowHosts": [ "showpc" ] }')
    Set-BundleConfig -Path $bundleJson -Url 'http://lightshow.example:3000' -Confirm:$false
    $b = Read-Text $bundleJson | ConvertFrom-Json
    Check 'party-visuals.json: -LightshowUrl changes the address' ($b.lightshow.url -eq 'http://lightshow.example:3000')
    Check 'party-visuals.json: the operator''s offsetMs and other settings stay' ($b.lightshow.pollMs -eq 2000 -and $b.graphics.offsetMs -eq 120 -and $b.graphics.maxFps -eq 60 -and
        (Read-Text $bundleJson) -match '"allowHosts": \[\s*"showpc"\s*\]')
    $fresh = Join-Path $cfg 'fresh.json'
    Set-BundleConfig -Path $fresh -Url 'http://lightshow.example:3000' -Confirm:$false
    $f = Read-Text $fresh | ConvertFrom-Json
    Check 'party-visuals.json, new: the token file and offsetMs 0' ($f.lightshow.tokenFile -eq 'cfg/party-visuals.token' -and $f.graphics.offsetMs -eq 0)

    # ------------------------------------------------------------ the install folder

    $plain = Join-Path $work 'plain'
    New-Item -ItemType Directory -Path $plain | Out-Null
    [System.IO.File]::WriteAllText((Join-Path $plain 'keep.txt'), 'keep')

    $r = Invoke-Installer @('-Root', $plain, '-LightshowUrl', 'http://lightshow.example:3000', '-WhatIf')
    Check 'install refuses a non-empty folder it did not set up' ($r.ExitCode -ne 0 -and $r.Output -match 'not set up by this script') $r.Output

    $new = Join-Path $work 'new'
    $r = Invoke-Installer @('-Root', $new, '-LightshowUrl', 'http://lightshow.example:3000', '-Autostart', '-WhatIf')
    Check 'install -WhatIf creates nothing' ($r.ExitCode -eq 0 -and -not (Test-Path -LiteralPath $new)) $r.Output

    $r = Invoke-Installer @('-Uninstall', '-Root', $plain)
    Check '-Uninstall refuses a folder without the marker and removes nothing' ($r.ExitCode -ne 0 -and (Test-Path -LiteralPath (Join-Path $plain 'keep.txt')) -and
        $r.Output -notmatch '==> Autostart') $r.Output

    $r = Invoke-Installer @('-Uninstall', '-Root', [System.IO.Path]::GetPathRoot($work))
    Check '-Uninstall refuses a drive root' ($r.ExitCode -ne 0 -and $r.Output -match 'drive or share root') $r.Output

    $target = Join-Path $work 'target'
    New-Marked $target
    [System.IO.File]::WriteAllText((Join-Path $target 'data.txt'), 'data')
    $link = Join-Path $work 'link'
    New-Item -ItemType Junction -Path $link -Target $target | Out-Null
    $r = Invoke-Installer @('-Uninstall', '-Root', $link)
    Check '-Uninstall refuses a root that is a junction' ($r.ExitCode -ne 0 -and (Test-Path -LiteralPath (Join-Path $target 'data.txt')) -and $r.Output -match 'is a link') $r.Output

    $victim = Join-Path $work 'victim'
    New-Item -ItemType Directory -Path $victim | Out-Null
    [System.IO.File]::WriteAllText((Join-Path $victim 'keep.txt'), 'keep')
    $marked = Join-Path $work 'marked'
    New-Marked $marked
    New-Item -ItemType Directory -Path (Join-Path $marked 'EclipseGraphics') | Out-Null
    New-Item -ItemType Junction -Path (Join-Path $marked 'EclipseGraphics\elsewhere') -Target $victim | Out-Null
    $r = Invoke-Installer @('-Uninstall', '-Root', $marked, '-WhatIf')
    Check '-Uninstall -WhatIf removes nothing' ($r.ExitCode -eq 0 -and (Test-Path -LiteralPath (Join-Path $marked 'party-visuals-install.json'))) $r.Output
    $r = Invoke-Installer @('-Uninstall', '-Root', $marked)
    Check '-Uninstall removes the folder it set up' ($r.ExitCode -eq 0 -and -not (Test-Path -LiteralPath $marked)) $r.Output
    Check '-Uninstall leaves what a junction in the folder points to' (Test-Path -LiteralPath (Join-Path $victim 'keep.txt'))
} finally {
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host ''
Write-Host "$script:passed passed, $script:failed failed"
if ($script:failed -gt 0) { exit 1 }
