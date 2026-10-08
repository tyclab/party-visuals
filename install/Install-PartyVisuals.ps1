#Requires -Version 5.1

<#
.SYNOPSIS
Installs the party-visuals NodeCG bundle beside EclipseGraphics on the show PC.

.DESCRIPTION
Run it as the user who runs the show, from a normal (not elevated) Windows
PowerShell 5.1 or PowerShell 7 window. It can be run again at any time: what is
already in place is left as it is.

  1. Checks for Node.js 22 or later and git. When one is missing it prints the
     winget command that installs it and stops.
  2. Clones EclipseGraphics at a pinned commit into <Root>\EclipseGraphics and
     installs its packages with npm ci, install scripts switched off. Only
     better-sqlite3, NodeCG's database driver, then runs its install script:
     it fetches or builds the native module NodeCG cannot start without. The
     other install scripts in that tree are a usage-statistics hook and two
     that compile from source only when no prebuilt binary is there (the
     prebuilt ones arrive as ordinary packages).
  3. Clones party-visuals at a pinned commit into
     <Root>\EclipseGraphics\bundles\party-visuals and installs its one package.
  4. Writes cfg\nodecg.json (NodeCG on 127.0.0.1:9090 only) and
     cfg\party-visuals.json (the lightshow's address and the token file),
     keeping any other setting already in them.
  5. Asks for the lightshow's access token (the input is hidden) and writes it
     to cfg\party-visuals.token, readable by the current user only. The token
     is not shown, logged or passed to another program.
  6. With -Autostart, registers a scheduled task that starts NodeCG when this
     user logs on.

-WhatIf shows what would change and changes nothing. -Uninstall removes the
scheduled task and the whole <Root> folder; it refuses a folder this script
did not set up (no marker file in it) and a <Root> that is a link.

.PARAMETER LightshowUrl
The lightshow server, http(s)://host:port, with no token in it. Needed on the
first run; later runs keep the address already configured unless it is given.

.PARAMETER Root
The install folder, a folder of its own (not a drive root). Default:
%LOCALAPPDATA%\PartyVisuals.

.PARAMETER EclipseGraphicsCommit
The EclipseGraphics commit to install (40 hex digits).

.PARAMETER BundleRef
The party-visuals commit (40 hex digits) or release tag (vX.Y.Z) to install.

.PARAMETER NewToken
Ask for the lightshow token again and replace the stored one.

.PARAMETER Autostart
Register (or update) the scheduled task that starts NodeCG at logon.

.PARAMETER Uninstall
Remove the scheduled task and the install folder, if this script set it up.

.EXAMPLE
powershell -ExecutionPolicy Bypass -File .\Install-PartyVisuals.ps1 -LightshowUrl http://lightshow.example:3000 -Autostart

.EXAMPLE
powershell -ExecutionPolicy Bypass -File .\Install-PartyVisuals.ps1 -LightshowUrl http://lightshow.example:3000 -WhatIf

.EXAMPLE
powershell -ExecutionPolicy Bypass -File .\Install-PartyVisuals.ps1 -Uninstall
#>
[CmdletBinding(SupportsShouldProcess = $true, DefaultParameterSetName = 'Install')]
param(
    [Parameter(ParameterSetName = 'Install')]
    [string] $LightshowUrl,

    [Parameter(ParameterSetName = 'Install')]
    [Parameter(ParameterSetName = 'Uninstall')]
    [ValidateNotNullOrEmpty()]
    [string] $Root = (Join-Path $env:LOCALAPPDATA 'PartyVisuals'),

    [Parameter(ParameterSetName = 'Install')]
    [ValidatePattern('^[0-9a-f]{40}$')]
    [string] $EclipseGraphicsCommit = 'c93ebef94e24c250502053cd12a9fa002be625c0',

    [Parameter(ParameterSetName = 'Install')]
    [ValidatePattern('^([0-9a-f]{40}|v\d+\.\d+\.\d+[0-9A-Za-z.-]*)$')]
    [string] $BundleRef = 'ce35af74cfb09bd124ce3baf1e86df7f830e6274',

    [Parameter(ParameterSetName = 'Install')]
    [switch] $NewToken,

    [Parameter(ParameterSetName = 'Install')]
    [switch] $Autostart,

    [Parameter(ParameterSetName = 'Uninstall', Mandatory = $true)]
    [switch] $Uninstall
)

Set-StrictMode -Version 2.0
$ErrorActionPreference = 'Stop'

$EclipseGraphicsRepo = 'https://github.com/LightD31/EclipseGraphics.git'
$BundleRepo = 'https://github.com/tyclab/party-visuals.git'
$TaskName = 'PartyVisuals NodeCG'
$MarkerName = 'party-visuals-install.json'
$NodecgHost = '127.0.0.1'
$NodecgPort = 9090
$MinNodeMajor = 22
# better-sqlite3 12.x, as EclipseGraphics' lockfile pins it, supports Node 20 to 26.
$MaxTestedNodeMajor = 26
$NativePackage = 'better-sqlite3'
# npm 11.16 knows the allow-scripts setting; npm 12 blocks install scripts without it.
$NpmAllowScriptsSince = [version]'11.16.0'
$TokenFileSetting = 'cfg/party-visuals.token'
$StampName = '.party-visuals-install'

# ---------------------------------------------------------------- helpers

function Write-Step([string] $Text) {
    Write-Host ''
    Write-Host "==> $Text" -ForegroundColor Cyan
}

function Write-Detail([string] $Text) {
    Write-Host "    $Text"
}

function Write-Utf8File([string] $Path, [string] $Text) {
    # UTF-8 without a byte order mark: Node's JSON.parse refuses one.
    [System.IO.File]::WriteAllText($Path, $Text, (New-Object System.Text.UTF8Encoding($false)))
}

function Invoke-Native {
    # Runs a program and fails on a non-zero exit code. -Capture returns its
    # standard output (standard error dropped); otherwise both go to the console.
    param(
        [Parameter(Mandatory = $true)] [string] $FilePath,
        [string[]] $Arguments = @(),
        [switch] $Capture,
        [switch] $AllowFailure
    )
    $saved = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        if ($Capture) {
            $out = & $FilePath @Arguments 2>$null
        } else {
            & $FilePath @Arguments | Out-Host
            $out = @()
        }
        $code = $LASTEXITCODE
    } finally {
        $ErrorActionPreference = $saved
    }
    if ($code -ne 0 -and -not $AllowFailure) {
        throw ('{0} {1} failed (exit code {2}).' -f [System.IO.Path]::GetFileName($FilePath), ($Arguments -join ' '), $code)
    }
    $text = (@($out) | ForEach-Object { "$_" }) -join "`n"
    New-Object PSObject -Property @{ ExitCode = $code; Text = $text.Trim() }
}

function ConvertTo-IndentedJson {
    # JSON with two-space indents, the same from Windows PowerShell and PowerShell 7.
    param($Value, [int] $Depth = 0)
    $pad = '  ' * ($Depth + 1)
    $end = '  ' * $Depth
    if ($null -eq $Value) { return 'null' }
    if ($Value -is [string]) { return (ConvertTo-Json -InputObject $Value -Compress) }
    if ($Value -is [bool]) { return $Value.ToString().ToLowerInvariant() }
    if ($Value -is [int] -or $Value -is [long] -or $Value -is [double] -or $Value -is [decimal]) {
        return [System.Convert]::ToString($Value, [System.Globalization.CultureInfo]::InvariantCulture)
    }
    $isMap = $Value -is [System.Collections.IDictionary]
    $isObject = $Value -is [System.Management.Automation.PSCustomObject]
    if ($isMap -or $isObject) {
        $pairs = New-Object System.Collections.Generic.List[object]
        if ($isMap) {
            foreach ($key in $Value.Keys) { $pairs.Add(@([string] $key, $Value[$key])) }
        } else {
            foreach ($p in $Value.PSObject.Properties) { $pairs.Add(@($p.Name, $p.Value)) }
        }
        $lines = @(foreach ($pair in $pairs) {
                '{0}{1}: {2}' -f $pad, (ConvertTo-Json -InputObject $pair[0] -Compress), (ConvertTo-IndentedJson $pair[1] ($Depth + 1))
            })
        if ($lines.Count -eq 0) { return '{}' }
        return "{`n" + ($lines -join ",`n") + "`n$end}"
    }
    if ($Value -is [System.Collections.IEnumerable]) {
        $lines = @(foreach ($item in $Value) { $pad + (ConvertTo-IndentedJson $item ($Depth + 1)) })
        if ($lines.Count -eq 0) { return '[]' }
        return "[`n" + ($lines -join ",`n") + "`n$end]"
    }
    throw "Cannot write a $($Value.GetType().FullName) as JSON."
}

function Read-JsonFile([string] $Path) {
    if (-not (Test-Path -LiteralPath $Path)) { return $null }
    $text = [System.IO.File]::ReadAllText($Path)
    if (-not $text.Trim()) { return $null }
    try {
        return ($text | ConvertFrom-Json)
    } catch {
        throw "$Path is not valid JSON; fix or delete it and run again."
    }
}

function Set-JsonProperty($Object, [string] $Name, $Value) {
    $Object | Add-Member -NotePropertyName $Name -NotePropertyValue $Value -Force
}

function Get-JsonProperty($Object, [string] $Name) {
    if ($null -eq $Object) { return $null }
    $p = $Object.PSObject.Properties[$Name]
    if ($null -eq $p) { return $null }
    return $p.Value
}

function Get-NormalizedRepoUrl([string] $Url) {
    $u = $Url.Trim().TrimEnd('/').ToLowerInvariant()
    if ($u.EndsWith('.git')) { $u = $u.Substring(0, $u.Length - 4) }
    return $u
}

function Test-LightshowUrl([string] $Url) {
    # The reason the address is refused, or $null. Only the documented form,
    # http(s)://host:port, passes; the bundle itself refuses credentials and
    # ?token= in it and would take a path.
    $uri = $null
    if (-not [System.Uri]::TryCreate($Url, [System.UriKind]::Absolute, [ref] $uri)) { return 'it is not an absolute address' }
    if ($uri.Scheme -ne 'http' -and $uri.Scheme -ne 'https') { return 'it must start with http:// or https://' }
    if ($uri.UserInfo) { return 'it must not carry a user name or password' }
    if ($uri.Query -or $uri.Fragment) { return 'it must not carry a query (no ?token=) or a fragment' }
    if ($uri.AbsolutePath -ne '/') { return 'it must be just http(s)://host:port' }
    return $null
}

function Get-CurrentUserSid {
    return [System.Security.Principal.WindowsIdentity]::GetCurrent().User
}

function Test-OwnerOnlyAcl([string] $Path) {
    $acl = Get-Acl -LiteralPath $Path
    $rules = @($acl.GetAccessRules($true, $true, [System.Security.Principal.SecurityIdentifier]))
    $sid = (Get-CurrentUserSid).Value
    return ($acl.AreAccessRulesProtected -and $rules.Count -eq 1 -and
        $rules[0].IdentityReference.Value -eq $sid -and
        $rules[0].AccessControlType -eq [System.Security.AccessControl.AccessControlType]::Allow)
}

function Set-OwnerOnlyAcl {
    # Inheritance off, one rule: full control for the current user.
    [CmdletBinding(SupportsShouldProcess = $true)]
    param([Parameter(Mandatory = $true)] [string] $Path)
    if (-not $PSCmdlet.ShouldProcess($Path, 'restrict access to the current user only')) { return }
    $security = New-Object System.Security.AccessControl.FileSecurity
    $security.SetAccessRuleProtection($true, $false)
    $rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
        (Get-CurrentUserSid),
        [System.Security.AccessControl.FileSystemRights]::FullControl,
        [System.Security.AccessControl.AccessControlType]::Allow)
    $security.AddAccessRule($rule)
    $file = New-Object System.IO.FileInfo($Path)
    if ($PSVersionTable.PSEdition -eq 'Core') {
        [System.IO.FileSystemAclExtensions]::SetAccessControl($file, $security)
    } else {
        $file.SetAccessControl($security)
    }
    if (-not (Test-OwnerOnlyAcl $Path)) { throw "Could not restrict $Path to the current user." }
}

function Write-TokenFile {
    # The file is emptied and locked down before the token goes in.
    [CmdletBinding(SupportsShouldProcess = $true)]
    param(
        [Parameter(Mandatory = $true)] [string] $Path,
        [Parameter(Mandatory = $true)] [System.Security.SecureString] $Token
    )
    if (-not $PSCmdlet.ShouldProcess($Path, 'write the lightshow token, readable by the current user only')) { return }
    $bstr = [System.Runtime.InteropServices.Marshal]::SecureStringToBSTR($Token)
    $plain = $null
    try {
        $plain = [System.Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr).Trim()
        if (-not $plain) { throw 'No token entered; nothing written.' }
        if ($plain -match '\s') { throw 'The token has a space or a line break in it; nothing written.' }
        Write-Utf8File $Path ''
        Set-OwnerOnlyAcl -Path $Path -Confirm:$false
        Write-Utf8File $Path $plain
    } finally {
        [System.Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
        $plain = $null
    }
}

function Test-PortListening([int] $Port) {
    $listeners = [System.Net.NetworkInformation.IPGlobalProperties]::GetIPGlobalProperties().GetActiveTcpListeners()
    return [bool] ($listeners | Where-Object { $_.Port -eq $Port } | Select-Object -First 1)
}

function Assert-NodecgStopped([string] $Why) {
    if (-not (Test-PortListening $NodecgPort)) { return }
    $msg = "Something listens on port ${NodecgPort}: NodeCG still running? Stop it first (Stop-ScheduledTask -TaskName '$TaskName', or close its window), then run again; $Why"
    if ($WhatIfPreference) { Write-Warning $msg } else { throw $msg }
}

# ---------------------------------------------------------------- steps

function Get-Prerequisites {
    $found = [ordered]@{ Node = $null; NodeMajor = 0; Npm = $null; NpmVersion = $null; Git = $null; Problems = @() }

    $node = Get-Command node.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($node) {
        $v = (Invoke-Native $node.Path @('--version') -Capture -AllowFailure).Text
        if ($v -match '^v(\d+)\.') { $found.NodeMajor = [int] $Matches[1] }
        if ($found.NodeMajor -ge $MinNodeMajor) {
            $found.Node = $node.Path
            Write-Detail "Node.js $v ($($node.Path))"
            if ($found.NodeMajor -gt $MaxTestedNodeMajor) {
                Write-Warning "Node.js $v is newer than the $MaxTestedNodeMajor.x EclipseGraphics' database driver supports; if its build fails, install Node.js $MaxTestedNodeMajor or older."
            }
        } else {
            $found.Problems += "Node.js $MinNodeMajor or later is needed, this PC has $v. Update it with:`n      winget upgrade --id OpenJS.NodeJS.LTS --exact"
        }
    } else {
        $found.Problems += "Node.js $MinNodeMajor or later is needed and is not installed. Install it with:`n      winget install --id OpenJS.NodeJS.LTS --exact"
    }

    if ($found.Node) {
        $npm = Get-Command npm.cmd -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($npm) {
            $found.Npm = $npm.Path
            $nv = (Invoke-Native $npm.Path @('--version') -Capture -AllowFailure).Text
            if ($nv -match '^(\d+)\.(\d+)\.(\d+)') { $found.NpmVersion = [version] ('{0}.{1}.{2}' -f $Matches[1], $Matches[2], $Matches[3]) }
            Write-Detail "npm $nv"
        } else {
            $found.Problems += "npm (part of Node.js) is not on PATH. Reinstall Node.js:`n      winget install --id OpenJS.NodeJS.LTS --exact --force"
        }
    }

    $git = Get-Command git.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($git) {
        $found.Git = $git.Path
        Write-Detail "$((Invoke-Native $git.Path @('--version') -Capture -AllowFailure).Text) ($($git.Path))"
    } else {
        $found.Problems += "git is needed and is not installed. Install it with:`n      winget install --id Git.Git --exact"
    }
    return $found
}

function Resolve-Commit([string] $Git, [string] $Dir, [string] $Ref) {
    $r = Invoke-Native $Git @('-C', $Dir, 'rev-parse', '--verify', '--quiet', "$Ref^{commit}") -Capture -AllowFailure
    if ($r.ExitCode -eq 0 -and $r.Text -match '^[0-9a-f]{40}$') { return $r.Text }
    return $null
}

function Sync-Checkout {
    # Clones $Url into $Dir if needed and checks out $Ref. $true when $Dir is at
    # $Ref afterwards, $false when -WhatIf left it out.
    [CmdletBinding(SupportsShouldProcess = $true)]
    param([string] $Git, [string] $Url, [string] $Ref, [string] $Dir)

    if (-not (Test-Path -LiteralPath (Join-Path $Dir '.git'))) {
        if ((Test-Path -LiteralPath $Dir) -and (Get-ChildItem -LiteralPath $Dir -Force | Select-Object -First 1)) {
            throw "$Dir exists and is not a git checkout. Move it away and run again."
        }
        if (-not $PSCmdlet.ShouldProcess($Dir, "clone $Url and check out $Ref")) { return $false }
        Invoke-Native $Git @('clone', '--quiet', '--', $Url, $Dir) | Out-Null
    } else {
        $origin = (Invoke-Native $Git @('-C', $Dir, 'remote', 'get-url', 'origin') -Capture -AllowFailure).Text
        if ((Get-NormalizedRepoUrl $origin) -ne (Get-NormalizedRepoUrl $Url)) {
            throw "$Dir is a checkout of '$origin', not of $Url. Move it away and run again."
        }
        # --no-optional-locks: status would otherwise rewrite the index, also under -WhatIf.
        $changes = (Invoke-Native $Git @('--no-optional-locks', '-C', $Dir, 'status', '--porcelain', '--untracked-files=no') -Capture).Text
        if ($changes) {
            throw "$Dir has local changes (git -C `"$Dir`" status). Put them aside and run again."
        }
    }

    $commit = Resolve-Commit $Git $Dir $Ref
    if (-not $commit) {
        if (-not $PSCmdlet.ShouldProcess($Dir, "fetch $Url to find $Ref, then check it out")) { return $false }
        Invoke-Native $Git @('-C', $Dir, 'fetch', '--quiet', '--tags', 'origin') | Out-Null
        $commit = Resolve-Commit $Git $Dir $Ref
        if (-not $commit) { throw "$Ref is not in $Url." }
    }
    if ((Resolve-Commit $Git $Dir 'HEAD') -eq $commit) {
        Write-Detail "$Dir is at $Ref"
        return $true
    }
    if (-not $PSCmdlet.ShouldProcess($Dir, "check out $Ref ($commit)")) { return $false }
    Invoke-Native $Git @('-C', $Dir, '-c', 'advice.detachedHead=false', 'checkout', '--quiet', '--detach', $commit) | Out-Null
    Write-Detail "$Dir checked out at $Ref"
    return $true
}

function Add-GitExclude {
    # Keeps what this script adds to a checkout out of its git status.
    [CmdletBinding(SupportsShouldProcess = $true)]
    param([string] $Dir, [string[]] $Patterns)
    $file = [System.IO.Path]::Combine($Dir, '.git', 'info', 'exclude')
    $have = @()
    if (Test-Path -LiteralPath $file) { $have = @([System.IO.File]::ReadAllLines($file)) }
    $missing = @($Patterns | Where-Object { $have -notcontains $_ })
    if ($missing.Count -eq 0) { return }
    if (-not $PSCmdlet.ShouldProcess($file, "add $($missing -join ', ')")) { return }
    New-Item -ItemType Directory -Path (Split-Path -Parent $file) -Force -Confirm:$false | Out-Null
    $text = ''
    if (Test-Path -LiteralPath $file) { $text = [System.IO.File]::ReadAllText($file) }
    if ($text -and -not $text.EndsWith("`n")) { $text += "`n" }
    Write-Utf8File $file ($text + "# Install-PartyVisuals.ps1`n" + (($missing | ForEach-Object { "$_`n" }) -join ''))
}

function Get-PackagesStamp([string] $Dir, [string] $Node) {
    # What the installed packages were installed from: the lockfile and Node's
    # native module version. $null when that cannot be known yet.
    $lock = Join-Path $Dir 'package-lock.json'
    if (-not (Test-Path -LiteralPath $lock) -or -not $Node) { return $null }
    $hash = (Get-FileHash -LiteralPath $lock -Algorithm SHA256).Hash.ToLowerInvariant()
    $abi = (Invoke-Native $Node @('-p', 'process.versions.modules') -Capture).Text
    return "lock=$hash node-modules=$abi"
}

function Test-PackagesCurrent([string] $Dir, [string] $Stamp) {
    if (-not $Stamp) { return $false }
    $file = [System.IO.Path]::Combine($Dir, 'node_modules', $StampName)
    if (-not (Test-Path -LiteralPath $file)) { return $false }
    return ([System.IO.File]::ReadAllText($file).Trim() -eq $Stamp)
}

function Install-EclipseGraphicsPackages {
    [CmdletBinding(SupportsShouldProcess = $true)]
    param([string] $Dir, $Tools)

    $stamp = Get-PackagesStamp $Dir $Tools.Node
    if (Test-PackagesCurrent $Dir $stamp) {
        Write-Detail 'packages are installed and current'
        return
    }
    Assert-NodecgStopped 'its packages are about to be replaced.'

    if ($PSCmdlet.ShouldProcess($Dir, 'npm ci with install scripts off (dev dependencies included: NodeCG is one)')) {
        Push-Location -LiteralPath $Dir
        try {
            Invoke-Native $Tools.Npm @('ci', '--ignore-scripts', '--include=dev', '--no-audit', '--no-fund') | Out-Null
        } finally { Pop-Location }
    }

    # The version the lockfile pins, read by Node (the lockfile is large).
    $version = $null
    if ($Tools.Node -and (Test-Path -LiteralPath (Join-Path $Dir 'package-lock.json'))) {
        Push-Location -LiteralPath $Dir
        try {
            $v = Invoke-Native $Tools.Node @('-p', "require('./package-lock.json').packages['node_modules/$NativePackage'].version") -Capture -AllowFailure
        } finally { Pop-Location }
        if ($v.ExitCode -eq 0 -and $v.Text -match '^\d+\.\d+\.\d+') { $version = $v.Text }
    }
    $spec = $NativePackage
    if ($version) { $spec = "$NativePackage@$version" }

    # npm 12 runs no dependency install script the project has not allowed.
    # The allowance goes in the checkout's own .npmrc (kept out of git), so
    # EclipseGraphics' package.json stays as upstream has it.
    if ($Tools.NpmVersion -and $Tools.NpmVersion -ge $NpmAllowScriptsSince) {
        if (-not $version -and -not $WhatIfPreference) { throw "$NativePackage is not in $Dir\package-lock.json." }
        $npmrc = Join-Path $Dir '.npmrc'
        $line = "allow-scripts=$spec"
        $current = @()
        if (Test-Path -LiteralPath $npmrc) { $current = @([System.IO.File]::ReadAllLines($npmrc)) }
        $allowed = (@($current | Where-Object { $_ -match '^\s*allow-scripts\s*=' }) -join "`n")
        if ($allowed -ne $line -and $PSCmdlet.ShouldProcess($npmrc, "allow the install script of $spec, and no other")) {
            $kept = @($current | Where-Object { $_ -notmatch '^\s*allow-scripts\s*=' })
            Write-Utf8File $npmrc ((@($kept + $line) -join "`n") + "`n")
        }
    } elseif (-not $Tools.NpmVersion) {
        Write-Detail "(with npm $NpmAllowScriptsSince or later, .npmrc in $Dir would allow the install script of $spec)"
    }

    if ($PSCmdlet.ShouldProcess($Dir, "npm rebuild $NativePackage (its install script, the only one run)")) {
        Write-Detail "npm may list the other packages whose install scripts stay off; that is intended."
        Push-Location -LiteralPath $Dir
        try {
            $r = Invoke-Native $Tools.Npm @('rebuild', $NativePackage, '--foreground-scripts') -AllowFailure
            # Load the native module once: npm reports success even when npm 12 skipped the script.
            $check = Invoke-Native $Tools.Node @('-e', "const D = require('$NativePackage'); new D(':memory:').close(); console.log('ok')") -Capture -AllowFailure
        } finally { Pop-Location }
        if ($r.ExitCode -ne 0 -or $check.Text -ne 'ok') {
            throw ("{0} did not build, so NodeCG cannot start. Its install script downloads a prebuilt module from GitHub; " +
                'check the network and run again. Without a prebuilt module for this Node.js it compiles one, which needs ' +
                'the Visual Studio C++ build tools.') -f $spec
        }
        Write-Detail "$spec built and loads"
        if (-not (Test-Path -LiteralPath ([System.IO.Path]::Combine($Dir, 'node_modules', 'nodecg', 'index.js')))) {
            throw "NodeCG is missing from $Dir\node_modules after npm ci."
        }
        Write-Utf8File ([System.IO.Path]::Combine($Dir, 'node_modules', $StampName)) "$stamp`n"
    }
}

function Install-BundlePackages {
    [CmdletBinding(SupportsShouldProcess = $true)]
    param([string] $Dir, $Tools)

    $stamp = Get-PackagesStamp $Dir $Tools.Node
    if (Test-PackagesCurrent $Dir $stamp) {
        Write-Detail 'packages are installed and current'
        return
    }
    Assert-NodecgStopped 'its packages are about to be replaced.'
    if (-not $PSCmdlet.ShouldProcess($Dir, 'npm ci without dev dependencies, install scripts off')) { return }
    Push-Location -LiteralPath $Dir
    try {
        Invoke-Native $Tools.Npm @('ci', '--omit=dev', '--ignore-scripts', '--no-audit', '--no-fund') | Out-Null
    } finally { Pop-Location }
    Write-Utf8File ([System.IO.Path]::Combine($Dir, 'node_modules', $StampName)) "$stamp`n"
}

function Set-NodecgConfig {
    # NodeCG on this machine only: OBS and Companion run here too.
    [CmdletBinding(SupportsShouldProcess = $true)]
    param([string] $Path)
    $cfg = Read-JsonFile $Path
    if ($null -eq $cfg) { $cfg = New-Object PSObject }
    if ((Get-JsonProperty $cfg 'host') -eq $NodecgHost -and (Get-JsonProperty $cfg 'port') -eq $NodecgPort) {
        Write-Detail "$Path keeps NodeCG on ${NodecgHost}:$NodecgPort"
        return
    }
    if (-not $PSCmdlet.ShouldProcess($Path, "set host $NodecgHost and port $NodecgPort, keep the other settings")) { return }
    Set-JsonProperty $cfg 'host' $NodecgHost
    Set-JsonProperty $cfg 'port' $NodecgPort
    Write-Utf8File $Path ((ConvertTo-IndentedJson $cfg) + "`n")
}

function Set-BundleConfig {
    # The lightshow address and the token file's name; never the token.
    [CmdletBinding(SupportsShouldProcess = $true)]
    param([string] $Path, [string] $Url)
    $cfg = Read-JsonFile $Path
    $isNew = $null -eq $cfg
    if ($isNew) {
        $cfg = New-Object PSObject
        Set-JsonProperty $cfg 'lightshow' (New-Object PSObject)
        Set-JsonProperty $cfg 'graphics' (New-Object PSObject -Property @{ offsetMs = 0 })
    }
    $lightshow = Get-JsonProperty $cfg 'lightshow'
    if ($null -eq $lightshow) {
        $lightshow = New-Object PSObject
        Set-JsonProperty $cfg 'lightshow' $lightshow
    }
    if (-not $Url) { $Url = Get-JsonProperty $lightshow 'url' }
    if (-not $Url) {
        $msg = 'The lightshow address is not configured yet: pass -LightshowUrl http(s)://<lightshow host>:<port>.'
        if ($WhatIfPreference) {
            Write-Warning $msg
            $Url = '<LightshowUrl>'
        } else { throw $msg }
    }
    if (-not $isNew -and (Get-JsonProperty $lightshow 'url') -eq $Url -and (Get-JsonProperty $lightshow 'tokenFile') -eq $TokenFileSetting) {
        Write-Detail "$Path points at $Url"
        return
    }
    if (-not $PSCmdlet.ShouldProcess($Path, "set lightshow.url $Url and lightshow.tokenFile $TokenFileSetting, keep the other settings")) { return }
    Set-JsonProperty $lightshow 'url' $Url
    Set-JsonProperty $lightshow 'tokenFile' $TokenFileSetting
    Write-Utf8File $Path ((ConvertTo-IndentedJson $cfg) + "`n")
    Write-Detail "$Path written (NodeCG reads it when it starts)"
}

function Set-Token {
    [CmdletBinding(SupportsShouldProcess = $true)]
    param([string] $Path, [switch] $Replace)
    $present = (Test-Path -LiteralPath $Path) -and ((Get-Item -LiteralPath $Path).Length -gt 0)
    if ($present -and -not $Replace) {
        Write-Detail "$Path is there (kept; -NewToken replaces it)"
        if (-not (Test-OwnerOnlyAcl $Path)) { Set-OwnerOnlyAcl -Path $Path }
        return
    }
    if (-not $PSCmdlet.ShouldProcess($Path, 'ask for the lightshow token (hidden input) and write it, readable by the current user only')) { return }
    Write-Host '    The lightshow token is under Settings > Server & access on the lightshow.'
    $secure = Read-Host -AsSecureString -Prompt '    Lightshow token (hidden)'
    Write-TokenFile -Path $Path -Token $secure -Confirm:$false
    Write-Detail "$Path written, readable by $([System.Security.Principal.WindowsIdentity]::GetCurrent().Name) only (NodeCG reads it when it starts)"
}

function Register-Autostart {
    [CmdletBinding(SupportsShouldProcess = $true)]
    param([string] $Node, [string] $StartScript, [string] $WorkDir)
    $user = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
    $what = "register: at $user's logon, run `"$Node`" `"$StartScript`" (NodeCG on ${NodecgHost}:$NodecgPort)"
    if (-not $PSCmdlet.ShouldProcess("scheduled task '$TaskName'", $what)) { return }
    $action = New-ScheduledTaskAction -Execute $Node -Argument ('"{0}"' -f $StartScript) -WorkingDirectory $WorkDir
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $user
    $principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive -RunLevel Limited
    # No time limit: the default would stop NodeCG after three days.
    $settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -MultipleInstances IgnoreNew -StartWhenAvailable -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)
    Register-ScheduledTask -TaskName $TaskName -Description 'NodeCG with EclipseGraphics and party-visuals, on 127.0.0.1:9090 (Install-PartyVisuals.ps1)' -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
    Write-Detail "scheduled task '$TaskName' registered"
}

function Get-AutostartTask {
    return (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue | Select-Object -First 1)
}

function Test-TaskUsesRoot($Task, [string] $Dir) {
    # With the separator, so ...\PartyVisuals does not match ...\PartyVisuals2.
    $inside = $Dir.TrimEnd('\') + '\'
    foreach ($a in @($Task.Actions)) {
        $text = "$($a.Arguments) $($a.WorkingDirectory)"
        if ($text.IndexOf($inside, [System.StringComparison]::OrdinalIgnoreCase) -ge 0) { return $true }
    }
    return $false
}

function Resolve-InstallRoot([string] $Path) {
    # The full path without a trailing separator. A drive or share root is
    # refused: -Uninstall removes the whole folder.
    $full = [System.IO.Path]::GetFullPath($ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Path))
    $trimmed = $full.TrimEnd('\', '/')
    $top = [System.IO.Path]::GetPathRoot($full).TrimEnd('\', '/')
    if ($trimmed.Length -le $top.Length) {
        throw "-Root $Path is a drive or share root; give the install a folder of its own."
    }
    return $trimmed
}

function Write-Marker {
    [CmdletBinding(SupportsShouldProcess = $true)]
    param([string] $Path)
    $want = New-Object PSObject
    Set-JsonProperty $want 'installedBy' 'Install-PartyVisuals.ps1'
    Set-JsonProperty $want 'eclipseGraphicsCommit' $EclipseGraphicsCommit
    Set-JsonProperty $want 'bundleRef' $BundleRef
    $text = (ConvertTo-IndentedJson $want) + "`n"
    if ((Test-Path -LiteralPath $Path) -and [System.IO.File]::ReadAllText($Path) -eq $text) { return }
    if (-not $PSCmdlet.ShouldProcess($Path, 'record what is installed here')) { return }
    Write-Utf8File $Path $text
}

# ---------------------------------------------------------------- install

function Invoke-Install {
    $principal = New-Object System.Security.Principal.WindowsPrincipal([System.Security.Principal.WindowsIdentity]::GetCurrent())
    if ($principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)) {
        Write-Warning 'This window is elevated. Run the script from a normal PowerShell window as the user who runs the show: the files, the token and the scheduled task belong to that user.'
    }

    if ($LightshowUrl) {
        $why = Test-LightshowUrl $LightshowUrl
        if ($why) { throw "-LightshowUrl is refused: $why. (The token goes in the token file, never in the address.)" }
        $LightshowUrl = ([System.Uri] $LightshowUrl).GetLeftPart([System.UriPartial]::Authority)
    }

    Write-Step 'Prerequisites'
    $tools = Get-Prerequisites
    if ($tools.Problems.Count -gt 0) {
        foreach ($p in $tools.Problems) { Write-Host "    $p" -ForegroundColor Yellow }
        Write-Host '    Open a new PowerShell window after installing (PATH changes), then run this script again.' -ForegroundColor Yellow
        if (-not $WhatIfPreference) { throw 'Prerequisites missing; nothing was changed.' }
        Write-Warning 'A real run stops here. The preview goes on as if they were installed.'
        if (-not $tools.Node) { $tools.Node = 'node.exe' }
        if (-not $tools.Npm) { $tools.Npm = 'npm.cmd' }
        if (-not $tools.Git) { $tools.Git = 'git.exe' }
    }
    $gitUsable = [bool] (Get-Command $tools.Git -ErrorAction SilentlyContinue)
    $nodeUsable = [bool] (Get-Command $tools.Node -ErrorAction SilentlyContinue)
    if (-not $nodeUsable) { $tools.Node = $null }

    $eg = Join-Path $Root 'EclipseGraphics'
    $bundle = [System.IO.Path]::Combine($eg, 'bundles', 'party-visuals')
    $cfg = Join-Path $eg 'cfg'

    Write-Step "Install folder $Root"
    if (Test-Path -LiteralPath $Root) {
        $hasMarker = Test-Path -LiteralPath (Join-Path $Root $MarkerName)
        $hasFiles = [bool] (Get-ChildItem -LiteralPath $Root -Force | Select-Object -First 1)
        if (-not $hasMarker -and $hasFiles) {
            throw "$Root has other files in it and was not set up by this script. Pick an empty or new folder with -Root."
        }
    } elseif ($PSCmdlet.ShouldProcess($Root, 'create the install folder')) {
        New-Item -ItemType Directory -Path $Root -Confirm:$false | Out-Null
    }
    Write-Marker -Path (Join-Path $Root $MarkerName)

    Write-Step "EclipseGraphics at $EclipseGraphicsCommit"
    $egReady = $false
    if ($gitUsable) {
        $egReady = Sync-Checkout -Git $tools.Git -Url $EclipseGraphicsRepo -Ref $EclipseGraphicsCommit -Dir $eg
    } else {
        $null = $PSCmdlet.ShouldProcess($eg, "clone $EclipseGraphicsRepo and check out $EclipseGraphicsCommit")
    }
    if ($egReady) { Add-GitExclude -Dir $eg -Patterns @('/bundles/', '/.npmrc') }
    else { $null = $PSCmdlet.ShouldProcess("$eg\.git\info\exclude", 'add /bundles/, /.npmrc') }
    Install-EclipseGraphicsPackages -Dir $eg -Tools $tools

    Write-Step "party-visuals at $BundleRef"
    if ($gitUsable -and $egReady) {
        $null = Sync-Checkout -Git $tools.Git -Url $BundleRepo -Ref $BundleRef -Dir $bundle
    } else {
        $null = $PSCmdlet.ShouldProcess($bundle, "clone $BundleRepo and check out $BundleRef")
    }
    Install-BundlePackages -Dir $bundle -Tools $tools

    Write-Step 'Configuration'
    if (-not (Test-Path -LiteralPath $cfg) -and $PSCmdlet.ShouldProcess($cfg, 'create the folder')) {
        New-Item -ItemType Directory -Path $cfg -Force -Confirm:$false | Out-Null
    }
    Set-NodecgConfig -Path (Join-Path $cfg 'nodecg.json')
    Set-BundleConfig -Path (Join-Path $cfg 'party-visuals.json') -Url $LightshowUrl

    Write-Step 'Lightshow token'
    Set-Token -Path (Join-Path $cfg 'party-visuals.token') -Replace:$NewToken

    Write-Step 'Autostart'
    $task = Get-AutostartTask
    if ($Autostart) {
        $node = $tools.Node
        if (-not $node) { $node = 'node.exe' }
        Register-Autostart -Node $node -StartScript (Join-Path $eg 'start.js') -WorkDir $eg
    } elseif ($task) {
        Write-Detail "scheduled task '$TaskName' is registered (left as it is)"
    } else {
        Write-Detail 'none (-Autostart registers a task that starts NodeCG at logon)'
    }

    $base = "http://${NodecgHost}:$NodecgPort"
    Write-Step 'Done'
    if ($WhatIfPreference) { Write-Detail 'Preview only: nothing was changed.' }
    Write-Detail "Dashboard:            $base/"
    Write-Detail "OBS, colour wash:     $base/bundles/party-visuals/graphics/wash.html"
    Write-Detail "OBS, beat bar:        $base/bundles/party-visuals/graphics/bar.html"
    Write-Detail "Companion (HTTP GET): $base/bundles/party-visuals/api/cmd/<wash|bar|all>/<command>"
    if ($Autostart -or $task) {
        Write-Detail "Start NodeCG now:     Start-ScheduledTask -TaskName '$TaskName'"
    } else {
        Write-Detail "Start NodeCG:         node `"$(Join-Path $eg 'start.js')`""
    }
}

# ---------------------------------------------------------------- uninstall

function Invoke-Uninstall {
    # The folder is checked before anything is removed, so a refusal leaves
    # the scheduled task in place too.
    $present = Test-Path -LiteralPath $Root
    if ($present) {
        if ((Get-Item -LiteralPath $Root -Force).Attributes -band [System.IO.FileAttributes]::ReparsePoint) {
            throw "$Root is a link to another folder, not a folder of its own; nothing removed. Remove the link and the folder it points to by hand."
        }
        if (-not (Test-Path -LiteralPath (Join-Path $Root $MarkerName))) {
            throw "$Root was not set up by this script (no $MarkerName in it); nothing removed."
        }
    }

    Write-Step 'Autostart'
    $task = Get-AutostartTask
    if ($task -and (Test-TaskUsesRoot $task $Root)) {
        if ($PSCmdlet.ShouldProcess("scheduled task '$TaskName'", 'stop and remove')) {
            Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
            Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
            Write-Detail "scheduled task '$TaskName' removed"
        }
    } elseif ($task) {
        Write-Detail "scheduled task '$TaskName' runs another folder; left as it is"
    } else {
        Write-Detail 'no scheduled task'
    }

    Write-Step "Install folder $Root"
    if (-not $present) {
        Write-Detail 'not there; nothing to remove'
        return
    }
    Assert-NodecgStopped 'its files are about to be removed.'
    if ($PSCmdlet.ShouldProcess($Root, 'remove the folder: EclipseGraphics, party-visuals, NodeCG''s configuration, database and logs, and the token file')) {
        # Remove-Item deletes a junction inside the folder, not the folder it
        # points to (test\install.test.ps1 checks it).
        Remove-Item -LiteralPath $Root -Recurse -Force -Confirm:$false
        Write-Detail "$Root removed"
    }
}

# ---------------------------------------------------------------- main

# Dot-sourced (test\install.test.ps1): the functions only.
if ($MyInvocation.InvocationName -eq '.') { return }

$Root = Resolve-InstallRoot $Root
if ($Uninstall) { Invoke-Uninstall } else { Invoke-Install }
