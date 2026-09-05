#Requires -Version 5.1
<#
.SYNOPSIS
  Shared helpers for Nyxveil Control Plane Windows deployment scripts.
#>

Set-StrictMode -Version Latest

$script:OperationalConfigProgramData = 'C:\ProgramData\Nyxveil\ControlPlane\operational.json'
$script:FirewallRulePrefix = 'Nyxveil Control Plane HTTPS '
$script:DefaultServiceName = 'NyxveilControlPlane'
$script:DefaultInstallDir = 'C:\Program Files\Nyxveil\ControlPlane'
$script:AspNetRuntimeMajor = 10

# -----------------------------------------------------------------------------
# Admin / paths
# -----------------------------------------------------------------------------

function Assert-Administrator {
    [CmdletBinding()]
    param()
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($id)
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Administrator privileges are required. Re-run this script from an elevated PowerShell session.'
    }
}

function Get-RepoRoot {
    [CmdletBinding()]
    [OutputType([string])]
    param()
    $licensingRoot = Split-Path -Parent $PSScriptRoot
    if (Test-Path (Join-Path $licensingRoot 'database\create_database.sql')) {
        return $licensingRoot
    }
    $repo = Split-Path -Parent $licensingRoot
    if (Test-Path (Join-Path $repo 'licensing\database\create_database.sql')) {
        return (Join-Path $repo 'licensing')
    }
    return $licensingRoot
}

function Get-ProgramDataRoot {
    [CmdletBinding()]
    [OutputType([string])]
    param()
    return 'C:\ProgramData\Nyxveil\ControlPlane'
}

function Get-DefaultPaths {
    [CmdletBinding()]
    param(
        [string]$InstallDir = $script:DefaultInstallDir
    )
    $pd = Get-ProgramDataRoot
    [pscustomobject]@{
        InstallDir = $InstallDir
        DataDir    = Join-Path $pd 'data'
        LogsDir    = Join-Path $pd 'logs'
        SecretsDir = Join-Path $pd 'secrets'
        KeysDir    = Join-Path $pd 'keys'
        BackupRoot = Join-Path $pd 'backups'
    }
}

# -----------------------------------------------------------------------------
# Operational config
# -----------------------------------------------------------------------------

function Get-OperationalConfigPath {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [string]$InstallDir = ''
    )
    if ($InstallDir -and (Test-Path (Join-Path $InstallDir 'config\operational.json'))) {
        return (Join-Path $InstallDir 'config\operational.json')
    }
    return $script:OperationalConfigProgramData
}

function New-OperationalConfigObject {
    [CmdletBinding()]
    param()
    [ordered]@{
        Port                  = 0
        BindAddress           = '0.0.0.0'
        PublicHostname        = ''
        PublicBaseUrl         = ''
        InstallDir            = $script:DefaultInstallDir
        DataDir               = (Join-Path (Get-ProgramDataRoot) 'data')
        LogsDir               = (Join-Path (Get-ProgramDataRoot) 'logs')
        SecretsDir            = (Join-Path (Get-ProgramDataRoot) 'secrets')
        ServiceName           = $script:DefaultServiceName
        ServiceAccount        = "NT SERVICE\$script:DefaultServiceName"
        DatabaseServer        = 'localhost'
        DatabaseName          = 'NyxveilControlPlane'
        DatabaseAuth          = 'Windows'
        DatabaseUser          = ''
        CertificateMode             = 'Store'
        CertificateValidationMode   = 'SystemTrust'
        CertificateThumbprint       = ''
        TrustSqlServerCertificate   = $true
        Encrypt                     = $true
        FirewallRuleName            = ''
        ExpectedSchemaVersion       = '1'
        CreatedUtc                  = (Get-Date).ToUniversalTime().ToString('o')
    }
}

function Invoke-NativeChecked {
    <#
    .SYNOPSIS
      Runs a scriptblock and fails if a native command left a non-zero LASTEXITCODE.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][scriptblock]$Script,
        [Parameter(Mandatory = $true)][string]$Name
    )
    $global:LASTEXITCODE = 0
    & $Script
    if ($null -ne $LASTEXITCODE -and $LASTEXITCODE -ne 0) {
        throw "$Name failed exit=$LASTEXITCODE"
    }
}

function Read-OperationalConfig {
    [CmdletBinding()]
    param(
        [string]$Path = ''
    )
    if ([string]::IsNullOrWhiteSpace($Path)) {
        $Path = Get-OperationalConfigPath
        if (-not (Test-Path $Path)) {
            $Path = $script:OperationalConfigProgramData
        }
    }
    if (-not (Test-Path $Path)) {
        throw "Operational config not found: $Path"
    }
    $raw = Get-Content -LiteralPath $Path -Raw -Encoding UTF8
    $obj = $raw | ConvertFrom-Json
    return $obj
}

function Write-OperationalConfig {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)]$Config,
        [string]$InstallDir = ''
    )
    $pdRoot = Get-ProgramDataRoot
    if (-not (Test-Path $pdRoot)) {
        New-Item -ItemType Directory -Force -Path $pdRoot | Out-Null
    }

    $json = ($Config | ConvertTo-Json -Depth 8)
    $primary = $script:OperationalConfigProgramData
    Set-Content -LiteralPath $primary -Value $json -Encoding UTF8

    if ([string]::IsNullOrWhiteSpace($InstallDir)) {
        if ($Config.InstallDir) { $InstallDir = [string]$Config.InstallDir }
    }
    if ($InstallDir) {
        $cfgDir = Join-Path $InstallDir 'config'
        if (-not (Test-Path $cfgDir)) {
            New-Item -ItemType Directory -Force -Path $cfgDir | Out-Null
        }
        $copyPath = Join-Path $cfgDir 'operational.json'
        Set-Content -LiteralPath $copyPath -Value $json -Encoding UTF8
    }
}

# -----------------------------------------------------------------------------
# Port helpers
# -----------------------------------------------------------------------------

function Assert-ValidPort {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][int]$Port
    )
    if ($Port -lt 1 -or $Port -gt 65535) {
        throw "Invalid port $Port. Port must be an integer between 1 and 65535."
    }
}

function Get-PortOccupancyDetails {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][int]$Port,
        [string]$BindAddress = '0.0.0.0'
    )
    $details = @()
    try {
        $conns = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    }
    catch {
        $conns = @()
    }
    if (-not $conns) { return $details }

    foreach ($c in @($conns)) {
        $pid = $c.OwningProcess
        $procName = '?'
        $svcName = $null
        try {
            $proc = Get-Process -Id $pid -ErrorAction Stop
            $procName = $proc.ProcessName
        }
        catch { }

        try {
            $svc = Get-CimInstance Win32_Service -Filter "ProcessId=$pid" -ErrorAction SilentlyContinue |
                Select-Object -First 1
            if ($svc) { $svcName = $svc.Name }
        }
        catch { }

        $details += [pscustomobject]@{
            LocalAddress  = $c.LocalAddress
            LocalPort     = $c.LocalPort
            Pid           = $pid
            ProcessName   = $procName
            OwningService = $svcName
        }
    }
    return $details
}

function Test-PortOwnedByService {
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory = $true)][int]$Port,
        [Parameter(Mandatory = $true)][string]$ServiceName,
        [string]$BindAddress = '0.0.0.0'
    )
    $details = @(Get-PortOccupancyDetails -Port $Port -BindAddress $BindAddress)
    if ($details.Count -eq 0) { return $false }
    foreach ($d in $details) {
        if ([string]$d.OwningService -ne $ServiceName) {
            return $false
        }
    }
    return $true
}

function Test-TcpPortAvailable {
    <#
    .SYNOPSIS
      Returns true when the port is free, or when every listener is our NyxveilControlPlane service
      (allowed for update/reconfigure). Foreign PIDs are conflicts.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory = $true)][int]$Port,
        [string]$BindAddress = '0.0.0.0',
        [string]$AllowServiceName = ''
    )
    Assert-ValidPort -Port $Port
    $details = @(Get-PortOccupancyDetails -Port $Port -BindAddress $BindAddress)
    if ($details.Count -eq 0) { return $true }

    $allOurs = $false
    if (-not [string]::IsNullOrWhiteSpace($AllowServiceName)) {
        $allOurs = $true
        foreach ($d in $details) {
            if ([string]$d.OwningService -ne $AllowServiceName) {
                $allOurs = $false
                break
            }
        }
        if ($allOurs) {
            Write-Host ("Port {0} is in use by our service '{1}' (allowed for update/reconfigure)." -f $Port, $AllowServiceName)
            return $true
        }
    }

    foreach ($d in $details) {
        $svcPart = if ($d.OwningService) { " / OwningService=$($d.OwningService)" } else { '' }
        Write-Host ("Port {0} is already in use by PID {1} / ProcessName={2}{3} (LocalAddress={4})" -f `
            $Port, $d.Pid, $d.ProcessName, $svcPart, $d.LocalAddress)
    }
    Write-Host 'Foreign listeners will NOT be stopped. Choose a different port.'
    return $false
}

# -----------------------------------------------------------------------------
# Firewall (Nyxveil-owned rules only)
# -----------------------------------------------------------------------------

function Get-NyxveilFirewallRuleName {
    [CmdletBinding()]
    [OutputType([string])]
    param([Parameter(Mandatory = $true)][int]$Port)
    return ("{0}{1}" -f $script:FirewallRulePrefix, $Port)
}

function Test-IsNyxveilFirewallRuleName {
    [CmdletBinding()]
    [OutputType([bool])]
    param([Parameter(Mandatory = $true)][string]$Name)
    return $Name.StartsWith($script:FirewallRulePrefix, [StringComparison]::OrdinalIgnoreCase)
}

function New-NyxveilFirewallRule {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][int]$Port,
        [string]$RuleName = ''
    )
    Assert-ValidPort -Port $Port
    if ([string]::IsNullOrWhiteSpace($RuleName)) {
        $RuleName = Get-NyxveilFirewallRuleName -Port $Port
    }
    if (-not (Test-IsNyxveilFirewallRuleName -Name $RuleName)) {
        throw "Refusing to create firewall rule '$RuleName'. Name must start with '$script:FirewallRulePrefix'."
    }

    $existing = Get-NetFirewallRule -DisplayName $RuleName -ErrorAction SilentlyContinue
    if ($existing) {
        Update-NyxveilFirewallRule -Port $Port -RuleName $RuleName
        return $RuleName
    }

    New-NetFirewallRule `
        -DisplayName $RuleName `
        -Name $RuleName `
        -Direction Inbound `
        -Action Allow `
        -Protocol TCP `
        -LocalPort $Port `
        -Profile Any `
        -Description 'Nyxveil Control Plane HTTPS inbound (installer-managed)' | Out-Null

    return $RuleName
}

function Update-NyxveilFirewallRule {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][int]$Port,
        [string]$RuleName = '',
        [string]$PreviousRuleName = ''
    )
    Assert-ValidPort -Port $Port
    if ([string]::IsNullOrWhiteSpace($RuleName)) {
        $RuleName = Get-NyxveilFirewallRuleName -Port $Port
    }
    if (-not (Test-IsNyxveilFirewallRuleName -Name $RuleName)) {
        throw "Refusing to update firewall rule '$RuleName'."
    }

    if ($PreviousRuleName -and $PreviousRuleName -ne $RuleName) {
        Remove-NyxveilFirewallRule -RuleName $PreviousRuleName
    }

    $existing = Get-NetFirewallRule -DisplayName $RuleName -ErrorAction SilentlyContinue
    if (-not $existing) {
        return (New-NyxveilFirewallRule -Port $Port -RuleName $RuleName)
    }

    Set-NetFirewallRule -DisplayName $RuleName -Enabled True -ErrorAction Stop | Out-Null
    $filter = Get-NetFirewallPortFilter -AssociatedNetFirewallRule $existing
    if ($filter -and [int]$filter.LocalPort -ne $Port) {
        # LocalPort is not always mutable in-place; recreate Nyxveil-owned rule only.
        Remove-NyxveilFirewallRule -RuleName $RuleName
        return (New-NyxveilFirewallRule -Port $Port -RuleName $RuleName)
    }
    return $RuleName
}

function Remove-NyxveilFirewallRule {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$RuleName
    )
    if (-not (Test-IsNyxveilFirewallRuleName -Name $RuleName)) {
        throw "Refusing to remove firewall rule '$RuleName'. Only rules named like '${script:FirewallRulePrefix}*' may be removed."
    }
    $existing = Get-NetFirewallRule -DisplayName $RuleName -ErrorAction SilentlyContinue
    if ($existing) {
        Remove-NetFirewallRule -DisplayName $RuleName -ErrorAction Stop
    }
}

# -----------------------------------------------------------------------------
# DPAPI secrets
# -----------------------------------------------------------------------------

function Initialize-ProtectedDataAssembly {
    [CmdletBinding()]
    param()
    Add-Type -AssemblyName System.Security -ErrorAction SilentlyContinue | Out-Null
}

function Protect-SecretString {
    [CmdletBinding()]
    [OutputType([byte[]])]
    param(
        [Parameter(Mandatory = $true)][string]$PlainText
    )
    Initialize-ProtectedDataAssembly
    $bytes = [Text.Encoding]::UTF8.GetBytes($PlainText)
    try {
        return [System.Security.Cryptography.ProtectedData]::Protect(
            $bytes,
            $null,
            [System.Security.Cryptography.DataProtectionScope]::LocalMachine)
    }
    finally {
        [Array]::Clear($bytes, 0, $bytes.Length)
    }
}

function Unprotect-SecretString {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory = $true)][byte[]]$ProtectedBytes
    )
    Initialize-ProtectedDataAssembly
    $plain = [System.Security.Cryptography.ProtectedData]::Unprotect(
        $ProtectedBytes,
        $null,
        [System.Security.Cryptography.DataProtectionScope]::LocalMachine)
    try {
        return [Text.Encoding]::UTF8.GetString($plain)
    }
    finally {
        [Array]::Clear($plain, 0, $plain.Length)
    }
}

function Write-ProtectedSecret {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$SecretsDir,
        [Parameter(Mandatory = $true)][string]$FileName,
        [Parameter(Mandatory = $true)][string]$PlainText
    )
    if (-not (Test-Path $SecretsDir)) {
        New-Item -ItemType Directory -Force -Path $SecretsDir | Out-Null
    }
    $path = Join-Path $SecretsDir $FileName
    $protected = Protect-SecretString -PlainText $PlainText
    [IO.File]::WriteAllBytes($path, $protected)
    return $path
}

function Read-ProtectedSecret {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory = $true)][string]$Path
    )
    if (-not (Test-Path $Path)) {
        throw "Protected secret file not found: $Path"
    }
    $bytes = [IO.File]::ReadAllBytes($Path)
    return (Unprotect-SecretString -ProtectedBytes $bytes)
}

function New-LicenseKekHex {
    [CmdletBinding()]
    [OutputType([string])]
    param()
    $buf = New-Object byte[] 32
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try {
        $rng.GetBytes($buf)
        return ([BitConverter]::ToString($buf) -replace '-', '').ToLowerInvariant()
    }
    finally {
        $rng.Dispose()
        [Array]::Clear($buf, 0, $buf.Length)
    }
}

function ConvertFrom-SecureStringPlain {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory = $true)][securestring]$SecureString
    )
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($SecureString)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    }
    finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
}

# -----------------------------------------------------------------------------
# sqlcmd
# -----------------------------------------------------------------------------

function Test-SqlCmdAvailable {
    [CmdletBinding()]
    [OutputType([bool])]
    param()
    return [bool](Get-Command sqlcmd -ErrorAction SilentlyContinue)
}

function Assert-SqlCmdAvailable {
    [CmdletBinding()]
    param(
        [string]$Context = 'database operations'
    )
    if (-not (Test-SqlCmdAvailable)) {
        throw @"
sqlcmd was not found on PATH but is required for $Context.
Install SQL Server Command Line Utilities (sqlcmd) and re-run.
Example: https://learn.microsoft.com/sql/tools/sqlcmd-utility
"@
    }
}

function Assert-ValidDatabaseName {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$DatabaseName
    )
    if ($DatabaseName -notmatch '^[A-Za-z_][A-Za-z0-9_-]{0,127}$') {
        throw @"
Invalid DatabaseName '$DatabaseName'.
Must match ^[A-Za-z_][A-Za-z0-9_-]{0,127}$ (letter/underscore start; letters, digits, underscore, hyphen; max 128).
"@
    }
}

function Test-IsLocalDatabaseServer {
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory = $true)][string]$DatabaseServer
    )
    $s = $DatabaseServer.Trim()
    if ([string]::IsNullOrWhiteSpace($s)) { return $true }
    # Strip instance: "host\INSTANCE" or "host,port"
    $hostPart = ($s -split '[\\,]', 2)[0].Trim()
    if ($hostPart -match '^(localhost|127\.0\.0\.1|\.|::1)$') { return $true }
    if ($hostPart -eq $env:COMPUTERNAME) { return $true }
    if ($hostPart -eq "$env:COMPUTERNAME.$env:USERDNSDOMAIN" -and $env:USERDNSDOMAIN) { return $true }
    try {
        $localHost = [System.Net.Dns]::GetHostName()
        if ($hostPart -eq $localHost) { return $true }
    }
    catch { }
    return $false
}

function Test-RemoteWindowsAuthSupported {
    <#
    .SYNOPSIS
      Auth matrix:
        LOCAL + NT SERVICE\* — OK
        REMOTE + Sql — OK
        REMOTE + gMSA (account ends with $) — OK if machine can resolve SID (no password param)
        REMOTE + ordinary domain user/password — NOT SUPPORTED
        REMOTE + NT SERVICE\* — NOT SUPPORTED
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory = $true)][string]$DatabaseServer,
        [Parameter(Mandatory = $true)][ValidateSet('Windows', 'Sql')][string]$DatabaseAuth,
        [Parameter(Mandatory = $true)][string]$ServiceAccount
    )
    if ($DatabaseAuth -ne 'Windows') { return $true }
    if (Test-IsLocalDatabaseServer -DatabaseServer $DatabaseServer) { return $true }

    $acct = $ServiceAccount.Trim()
    if ($acct -like 'NT SERVICE\*') {
        throw @"
Remote SQL Server '$DatabaseServer' with Windows Auth is not supported for virtual service account '$ServiceAccount'.
NT SERVICE\* identities are local-only and cannot authenticate to a remote SQL host.

Choose one of:
  1) DatabaseAuth=Sql (SQL login + DPAPI sql-password.dpapi), or
  2) A gMSA (DOMAIN\svc-nyxveil`$ ) with SQL Windows login granted, or
  3) Install SQL Server locally and keep Windows Auth + NT SERVICE\NyxveilControlPlane.
"@
    }

    # gMSA SAM account names end with '$'
    if ($acct.EndsWith('$')) {
        try {
            $nt = New-Object System.Security.Principal.NTAccount($acct)
            $null = $nt.Translate([System.Security.Principal.SecurityIdentifier])
            return $true
        }
        catch {
            throw @"
Remote Windows Auth gMSA '$ServiceAccount' could not be resolved on this machine.
Ensure the gMSA is installed/usable here (no password parameter is accepted).
$($_.Exception.Message)
"@
        }
    }

    throw @"
Remote SQL Server '$DatabaseServer' with Windows Auth is not supported for ordinary domain account '$ServiceAccount'.
Installer does not accept a domain user password for the service identity.

Supported combinations:
  1) LOCAL SQL + NT SERVICE\NyxveilControlPlane
  2) REMOTE SQL + DatabaseAuth=Sql
  3) REMOTE SQL + gMSA (account name ending with `$ )
"@
}

function Resolve-TrustSqlServerCertificatePolicy {
    <#
    .SYNOPSIS
      Local SQL defaults Trust=true; remote defaults Trust=false.
      Remote + Trust=true requires explicit -TrustSqlServerCertificate (with warning).
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory = $true)][string]$DatabaseServer,
        [switch]$TrustSqlServerCertificate,
        [switch]$TrustSqlServerCertificateSpecified
    )
    $isLocal = Test-IsLocalDatabaseServer -DatabaseServer $DatabaseServer
    if ($TrustSqlServerCertificateSpecified) {
        if (-not $isLocal -and $TrustSqlServerCertificate) {
            Write-Warning @"
TrustSqlServerCertificate=true for remote SQL Server '$DatabaseServer' disables SQL TLS certificate validation.
Use only with an explicit operational decision (lab / pinned private CA scenarios).
"@
        }
        return [bool]$TrustSqlServerCertificate
    }
    return [bool]$isLocal
}

function Get-NyxveilDatabaseSettings {
    <#
    .SYNOPSIS
      Resolves Server, Database, Auth, User, Encrypt, TrustSqlServerCertificate from
      operational.json and appsettings.Production.json (operational wins when set).
    #>
    [CmdletBinding()]
    param(
        [string]$InstallDir = '',
        [string]$OperationalPath = ''
    )

    $op = $null
    try {
        if ($OperationalPath) { $op = Read-OperationalConfig -Path $OperationalPath }
        elseif ($InstallDir) {
            $candidate = Get-OperationalConfigPath -InstallDir $InstallDir
            if (Test-Path -LiteralPath $candidate) { $op = Read-OperationalConfig -Path $candidate }
            else { $op = Read-OperationalConfig }
        }
        else { $op = Read-OperationalConfig }
    }
    catch { }

    if ([string]::IsNullOrWhiteSpace($InstallDir) -and $op -and $op.InstallDir) {
        $InstallDir = [string]$op.InstallDir
    }

    $settings = $null
    if ($InstallDir) {
        $appPath = Join-Path $InstallDir 'appsettings.Production.json'
        if (Test-Path -LiteralPath $appPath) {
            $settings = Get-Content -LiteralPath $appPath -Raw -Encoding UTF8 | ConvertFrom-Json
        }
    }

    $server = 'localhost'
    $database = 'NyxveilControlPlane'
    $auth = 'Windows'
    $user = ''
    $encrypt = $true
    $trust = $true

    if ($settings -and $settings.ConnectionStrings -and $settings.ConnectionStrings.ControlPlane) {
        $cs = [string]$settings.ConnectionStrings.ControlPlane
        if ($cs -match '(?i)(?:Server|Data Source)=([^;]+)') { $server = $Matches[1].Trim() }
        if ($cs -match '(?i)(?:Database|Initial Catalog)=([^;]+)') { $database = $Matches[1].Trim() }
        if ($cs -match '(?i)User ID=([^;]+)') { $user = $Matches[1].Trim() }
        if ($cs -match '(?i)Trusted_Connection=True' -or $cs -match '(?i)Integrated Security=True') {
            $auth = 'Windows'
        }
        elseif ($user) {
            $auth = 'Sql'
        }
        if ($cs -match '(?i)Encrypt=(True|False|Mandatory|Optional|Strict)') {
            $encTok = $Matches[1]
            $encrypt = ($encTok -notin @('False', 'Optional'))
        }
        if ($cs -match '(?i)TrustServerCertificate=(True|False)') {
            $trust = ($Matches[1] -eq 'True')
        }
    }

    if ($settings -and $settings.Database) {
        if ($null -ne $settings.Database.Encrypt) { $encrypt = [bool]$settings.Database.Encrypt }
        if ($null -ne $settings.Database.TrustSqlServerCertificate) {
            $trust = [bool]$settings.Database.TrustSqlServerCertificate
        }
    }

    if ($op) {
        if ($op.DatabaseServer) { $server = [string]$op.DatabaseServer }
        if ($op.DatabaseName) { $database = [string]$op.DatabaseName }
        if ($op.DatabaseAuth) { $auth = [string]$op.DatabaseAuth }
        if ($op.PSObject.Properties.Name -contains 'DatabaseUser' -and $op.DatabaseUser) {
            $user = [string]$op.DatabaseUser
        }
        if ($op.PSObject.Properties.Name -contains 'TrustSqlServerCertificate' -and $null -ne $op.TrustSqlServerCertificate) {
            $trust = [bool]$op.TrustSqlServerCertificate
        }
        if ($op.PSObject.Properties.Name -contains 'Encrypt' -and $null -ne $op.Encrypt) {
            $encrypt = [bool]$op.Encrypt
        }
    }

    if ($auth -eq 'Sql' -and [string]::IsNullOrWhiteSpace($user) -and $settings) {
        $cs = [string]$settings.ConnectionStrings.ControlPlane
        if ($cs -match '(?i)User ID=([^;]+)') { $user = $Matches[1].Trim() }
    }

    return [pscustomobject]@{
        Server                    = $server
        Database                  = $database
        Auth                      = $auth
        User                      = $user
        Encrypt                   = [bool]$encrypt
        TrustSqlServerCertificate = [bool]$trust
        InstallDir                = $InstallDir
    }
}

function Get-NyxveilSqlcmdArgs {
    <#
    .SYNOPSIS
      Builds sqlcmd argument list. Never includes -P; Sql auth uses process env SQLCMDPASSWORD.
      Applies the same TrustServerCertificate policy as the app (-C when Trust=true).
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Server,
        [string]$InputFile = '',
        [string]$Query = '',
        [string]$DatabaseName = 'NyxveilControlPlane',
        [ValidateSet('Windows', 'Sql')][string]$DatabaseAuth = 'Windows',
        [string]$DatabaseUser = '',
        [int]$QueryTimeout = 0,
        [string[]]$ExtraArgs = @(),
        [bool]$TrustSqlServerCertificate = $false,
        [bool]$Encrypt = $true
    )

    Assert-ValidDatabaseName -DatabaseName $DatabaseName
    if ([string]::IsNullOrWhiteSpace($InputFile) -and [string]::IsNullOrWhiteSpace($Query)) {
        throw 'Get-NyxveilSqlcmdArgs requires -InputFile or -Query.'
    }

    $sqlArgs = New-Object 'System.Collections.Generic.List[string]'
    [void]$sqlArgs.Add('-b')
    [void]$sqlArgs.Add('-S')
    [void]$sqlArgs.Add($Server)
    [void]$sqlArgs.Add('-v')
    [void]$sqlArgs.Add("DatabaseName=$DatabaseName")

    if ($QueryTimeout -gt 0) {
        [void]$sqlArgs.Add('-t')
        [void]$sqlArgs.Add("$QueryTimeout")
    }

    if ($DatabaseAuth -eq 'Sql') {
        if ([string]::IsNullOrWhiteSpace($DatabaseUser)) {
            throw 'SQL Auth requires -DatabaseUser (password via SQLCMDPASSWORD env, never -P).'
        }
        [void]$sqlArgs.Add('-U')
        [void]$sqlArgs.Add($DatabaseUser)
    }
    else {
        [void]$sqlArgs.Add('-E')
    }

    # Align with app TrustSqlServerCertificate (sqlcmd -C).
    if ($TrustSqlServerCertificate) {
        [void]$sqlArgs.Add('-C')
    }

    if ($InputFile) {
        if (-not (Test-Path -LiteralPath $InputFile)) { throw "SQL script not found: $InputFile" }
        [void]$sqlArgs.Add('-i')
        [void]$sqlArgs.Add($InputFile)
    }
    else {
        [void]$sqlArgs.Add('-Q')
        [void]$sqlArgs.Add($Query)
    }

    foreach ($a in @($ExtraArgs)) {
        if (-not [string]::IsNullOrWhiteSpace($a)) { [void]$sqlArgs.Add($a) }
    }

    return [pscustomobject]@{
        Arguments                 = @($sqlArgs.ToArray())
        DatabaseAuth              = $DatabaseAuth
        UsesSqlPasswordEnv        = ($DatabaseAuth -eq 'Sql')
        TrustSqlServerCertificate = [bool]$TrustSqlServerCertificate
        Encrypt                   = [bool]$Encrypt
    }
}

function Invoke-NyxveilSql {
    <#
    .SYNOPSIS
      Unified sqlcmd helper (Windows or Sql auth). Sql auth sets SQLCMDPASSWORD for this process only.
      Uses the same Trust/Encrypt policy as the Control Plane app when provided.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Server,
        [string]$InputFile = '',
        [string]$Query = '',
        [string]$DatabaseName = 'NyxveilControlPlane',
        [ValidateSet('Windows', 'Sql')][string]$DatabaseAuth = 'Windows',
        [string]$DatabaseUser = '',
        [securestring]$DatabasePassword,
        [int]$QueryTimeout = 0,
        [string[]]$ExtraArgs = @(),
        [bool]$TrustSqlServerCertificate = $false,
        [bool]$Encrypt = $true
    )

    Assert-SqlCmdAvailable -Context 'sqlcmd execution'
    $built = $null
    $plainPass = $null
    $prevSqlCmdPassword = $null
    $hadPrev = $false
    $usedSqlEnv = $false
    try {
        $built = Get-NyxveilSqlcmdArgs -Server $Server -InputFile $InputFile -Query $Query `
            -DatabaseName $DatabaseName -DatabaseAuth $DatabaseAuth -DatabaseUser $DatabaseUser `
            -QueryTimeout $QueryTimeout -ExtraArgs $ExtraArgs `
            -TrustSqlServerCertificate $TrustSqlServerCertificate -Encrypt $Encrypt

        if ($built.UsesSqlPasswordEnv) {
            if (-not $DatabasePassword) {
                throw 'SQL Auth requires -DatabasePassword (passed via SQLCMDPASSWORD; never -P on the command line).'
            }
            if (Test-Path Env:SQLCMDPASSWORD) {
                $hadPrev = $true
                $prevSqlCmdPassword = $env:SQLCMDPASSWORD
            }
            $plainPass = ConvertFrom-SecureStringPlain -SecureString $DatabasePassword
            $env:SQLCMDPASSWORD = $plainPass
            $usedSqlEnv = $true
        }

        $argList = $built.Arguments
        & sqlcmd @argList
        if ($null -eq $LASTEXITCODE -or $LASTEXITCODE -ne 0) {
            throw "sqlcmd failed with exit code $LASTEXITCODE (fail-closed)."
        }
    }
    finally {
        if ($usedSqlEnv) {
            if ($hadPrev) {
                $env:SQLCMDPASSWORD = $prevSqlCmdPassword
            }
            else {
                Remove-Item Env:\SQLCMDPASSWORD -ErrorAction SilentlyContinue
            }
        }
        if ($null -ne $plainPass) { $plainPass = $null }
        $prevSqlCmdPassword = $null
    }
}

function Invoke-SqlCmdFailClosed {
    <#
    .SYNOPSIS
      Compatibility wrapper — prefer Invoke-NyxveilSql.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Server,
        [string]$InputFile = '',
        [string]$Query = '',
        [string]$DatabaseName = 'NyxveilControlPlane',
        [ValidateSet('Windows', 'Sql')][string]$DatabaseAuth = 'Windows',
        [string]$DatabaseUser = '',
        [securestring]$DatabasePassword,
        [int]$QueryTimeout = 0,
        [bool]$TrustSqlServerCertificate = $false,
        [bool]$Encrypt = $true
    )
    Invoke-NyxveilSql -Server $Server -InputFile $InputFile -Query $Query `
        -DatabaseName $DatabaseName -DatabaseAuth $DatabaseAuth `
        -DatabaseUser $DatabaseUser -DatabasePassword $DatabasePassword `
        -QueryTimeout $QueryTimeout `
        -TrustSqlServerCertificate $TrustSqlServerCertificate -Encrypt $Encrypt
}

function New-CreateDatabaseScriptCopy {
    <#
    .SYNOPSIS
      Ensures create_database.sql uses sqlcmd $(DatabaseName) for CREATE/USE.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory = $true)][string]$SourcePath,
        [Parameter(Mandatory = $true)][string]$DatabaseName,
        [string]$DestinationPath = ''
    )
    if (-not (Test-Path $SourcePath)) {
        throw "create_database.sql not found: $SourcePath"
    }
    if ([string]::IsNullOrWhiteSpace($DestinationPath)) {
        $DestinationPath = Join-Path $env:TEMP ("nyxveil-create-db-{0}.sql" -f [guid]::NewGuid().ToString('N'))
    }

    $content = Get-Content -LiteralPath $SourcePath -Raw -Encoding UTF8
    if ($content -notmatch '\$\(DatabaseName\)') {
        # Rewrite legacy DECLARE/USE forms so -v DatabaseName= works.
        $content = $content -replace '(?m)^\s*DECLARE\s+@DatabaseName\s+sysname\s*=\s*N''[^'']+'';\s*$',
            'DECLARE @DatabaseName sysname = N''$(DatabaseName)'';'
        $content = $content -replace '(?m)^USE\s+\[[^\]]+\]\s*;\s*$',
            'USE [$(DatabaseName)];'
        if ($content -notmatch ':setvar\s+DatabaseName') {
            $header = @'
-- Auto-prepared for sqlcmd -v DatabaseName=...
:setvar DatabaseName NyxveilControlPlane
GO

'@
            $content = $header + $content
        }
    }

    Set-Content -LiteralPath $DestinationPath -Value $content -Encoding UTF8
    return $DestinationPath
}

# -----------------------------------------------------------------------------
# HTTPS health
# -----------------------------------------------------------------------------

function Get-HealthTarget {
    <#
    .SYNOPSIS
      Builds a local health target: TLS hostname = PublicHostname; TCP connect via 127.0.0.1 when needed.
    .NOTES
      Prefer Nyxveil.ControlPlane.Web.exe self-test / self-test-http over curl/iwr.
      When using curl, use --resolve so certificate hostname validation still uses PublicHostname.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][int]$Port,
        [string]$PublicHostname = 'localhost',
        [string]$ConnectAddress = '127.0.0.1'
    )
    Assert-ValidPort -Port $Port
    $hostName = $PublicHostname.Trim()
    if ([string]::IsNullOrWhiteSpace($hostName)) { $hostName = 'localhost' }
    if ($hostName -match '^https?://') {
        $uri = [Uri]$hostName
        $hostName = $uri.Host
    }
    $url = "https://${hostName}:${Port}"
    [pscustomobject]@{
        PublicHostname = $hostName
        Port           = $Port
        ConnectAddress = $ConnectAddress
        BaseUrl        = $url
        CurlResolve    = "${hostName}:${Port}:${ConnectAddress}"
    }
}

function Get-LocalHealthBaseUrl {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory = $true)][int]$Port,
        [string]$PublicHostname = 'localhost'
    )
    return (Get-HealthTarget -Port $Port -PublicHostname $PublicHostname).BaseUrl
}

function Get-NyxveilWebExePath {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [string]$InstallDir = ''
    )
    if ([string]::IsNullOrWhiteSpace($InstallDir)) {
        try {
            $op = Read-OperationalConfig
            if ($op -and $op.InstallDir) { $InstallDir = [string]$op.InstallDir }
        }
        catch { }
    }
    if ([string]::IsNullOrWhiteSpace($InstallDir)) {
        $InstallDir = $script:DefaultInstallDir
    }
    $exe = Join-Path $InstallDir 'Nyxveil.ControlPlane.Web.exe'
    if (-not (Test-Path -LiteralPath $exe)) {
        throw "Web host not found: $exe"
    }
    return $exe
}

function Invoke-NyxveilWebCli {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$InstallDir,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [securestring]$StdinSecure,
        [int]$TimeoutMs = 120000
    )
    $exe = Get-NyxveilWebExePath -InstallDir $InstallDir
    $quoted = foreach ($a in $Arguments) {
        if ($null -eq $a) { continue }
        $s = [string]$a
        if ($s -match '[\s"]') {
            '"' + ($s.Replace('"', '\"')) + '"'
        }
        else { $s }
    }
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $exe
    $psi.Arguments = ($quoted -join ' ')
    $psi.WorkingDirectory = $InstallDir
    $psi.UseShellExecute = $false
    $psi.RedirectStandardInput = [bool]$StdinSecure
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.CreateNoWindow = $true
    $psi.EnvironmentVariables['ASPNETCORE_ENVIRONMENT'] = 'Production'

    $p = New-Object System.Diagnostics.Process
    $p.StartInfo = $psi
    [void]$p.Start()
    if ($StdinSecure) {
        $plain = ConvertFrom-SecureStringPlain -SecureString $StdinSecure
        try {
            $p.StandardInput.WriteLine($plain)
            $p.StandardInput.Close()
        }
        finally { $plain = $null }
    }
    $stdout = $p.StandardOutput.ReadToEnd()
    $stderr = $p.StandardError.ReadToEnd()
    if (-not $p.WaitForExit($TimeoutMs)) {
        try { $p.Kill() } catch { }
        throw "CLI timed out: $exe $($Arguments -join ' ')"
    }
    return [pscustomobject]@{
        ExitCode = $p.ExitCode
        StdOut   = $stdout
        StdErr   = $stderr
    }
}

function Invoke-NyxveilSelfTestCli {
    <#
    .SYNOPSIS
      Prefers published exe self-test-http (hostname-aware) then self-test.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory = $true)][string]$InstallDir,
        [string]$PublicHostname = ''
    )
    $null = Get-NyxveilWebExePath -InstallDir $InstallDir

    $candidates = New-Object System.Collections.ArrayList
    if (-not [string]::IsNullOrWhiteSpace($PublicHostname)) {
        [void]$candidates.Add(@('self-test-http', $PublicHostname))
        [void]$candidates.Add(@('self-test', '--hostname', $PublicHostname))
    }
    [void]$candidates.Add(@('self-test'))

    foreach ($cliArgs in $candidates) {
        $result = Invoke-NyxveilWebCli -InstallDir $InstallDir -Arguments ([string[]]$cliArgs)
        if ($result.ExitCode -eq 0) {
            if ($result.StdOut) { Write-Host ($result.StdOut.TrimEnd()) }
            return $true
        }
        $combined = ("{0}`n{1}" -f $result.StdOut, $result.StdErr)
        if ($combined -match '(?i)unknown|usage:|not (a )?valid|unrecognized') {
            Write-Verbose "CLI candidate not supported: $($cliArgs -join ' ')"
            continue
        }
        if ($result.StdOut) { Write-Host ($result.StdOut.TrimEnd()) }
        if ($result.StdErr) { Write-Host ($result.StdErr.TrimEnd()) }
        Write-Verbose "CLI self-test exit $($result.ExitCode) for: $($cliArgs -join ' ')"
        return $false
    }
    Write-Warning "No supported self-test CLI under InstallDir=$InstallDir"
    return $false
}

function Test-HttpsHealthHttp {
    <#
    .SYNOPSIS
      Hostname-aware HTTPS probe via curl --resolve (no -k / SkipCertificateCheck).
    .NOTES
      Prefer Invoke-NyxveilSelfTestCli. curl/iwr is a fallback only when the cert chain is trusted
      for PublicHostname (self-signed lab certs typically fail TLS validation by design).
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory = $true)][int]$Port,
        [string]$PublicHostname = 'localhost',
        [string]$Path = '/health/live',
        [int]$TimeoutSec = 15
    )
    $target = Get-HealthTarget -Port $Port -PublicHostname $PublicHostname
    $url = ($target.BaseUrl.TrimEnd('/')) + $Path
    $curl = Get-Command curl.exe -ErrorAction SilentlyContinue
    if ($curl) {
        $out = & curl.exe -sS -f --max-time $TimeoutSec --resolve $target.CurlResolve $url 2>&1
        if ($LASTEXITCODE -eq 0) { return $true }
        Write-Verbose "curl health failed for $url : $out"
        return $false
    }

    # PowerShell fallback — validates TLS against PublicHostname (no TrustAll).
    try {
        $resp = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec $TimeoutSec
        return ($resp.StatusCode -ge 200 -and $resp.StatusCode -lt 300)
    }
    catch {
        Write-Verbose "iwr health failed for $url : $($_.Exception.Message)"
        return $false
    }
}

function Test-HttpsHealth {
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory = $true)][string]$BaseUrl,
        [string]$Path = '/health/live',
        [int]$TimeoutSec = 15,
        [string]$PublicHostname = '',
        [int]$Port = 0
    )
    # Prefer hostname-aware helper when Port is known.
    if ($Port -gt 0) {
        $hn = $PublicHostname
        if ([string]::IsNullOrWhiteSpace($hn) -and $BaseUrl -match '^https://([^/:]+)') {
            $hn = $Matches[1]
        }
        return (Test-HttpsHealthHttp -Port $Port -PublicHostname $hn -Path $Path -TimeoutSec $TimeoutSec)
    }
    $url = ($BaseUrl.TrimEnd('/')) + $Path
    try {
        $resp = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec $TimeoutSec
        return ($resp.StatusCode -ge 200 -and $resp.StatusCode -lt 300)
    }
    catch {
        Write-Verbose "Health check failed for $url : $($_.Exception.Message)"
        return $false
    }
}

function Test-HttpsHealthLocal {
    <#
    .SYNOPSIS
      Local post-install health: prefer published exe self-test; optional HTTP without cert bypass.
      SystemTrust: requires live+ready HTTPS with system TLS validation (no thumbprint-only escape).
      SelfSignedPinned: requires CLI self-test (pin+hostname); HTTP chain probe is not required.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory = $true)][int]$Port,
        [string]$PublicHostname = 'localhost',
        [string]$InstallDir = '',
        [string]$CertificateMode = 'Store',
        [ValidateSet('SystemTrust', 'SelfSignedPinned', 'SelfSigned', 'Store', 'Pfx', '')]
        [string]$CertificateValidationMode = '',
        [switch]$RequireHttp
    )

    $validation = $CertificateValidationMode
    if ([string]::IsNullOrWhiteSpace($validation)) {
        if ($CertificateMode -eq 'SelfSigned' -or $CertificateMode -eq 'SelfSignedPinned') {
            $validation = 'SelfSignedPinned'
        }
        else {
            $validation = 'SystemTrust'
        }
    }
    elseif ($validation -eq 'SelfSigned') {
        $validation = 'SelfSignedPinned'
    }

    $cliOk = $false
    if (-not [string]::IsNullOrWhiteSpace($InstallDir) -and (Test-Path (Join-Path $InstallDir 'Nyxveil.ControlPlane.Web.exe'))) {
        $cliOk = Invoke-NyxveilSelfTestCli -InstallDir $InstallDir -PublicHostname $PublicHostname
        if (-not $cliOk) {
            Write-Host 'CLI self-test failed.'
            return $false
        }
        Write-Host 'CLI self-test passed.'
    }

    if ($validation -eq 'SelfSignedPinned') {
        if ($cliOk) { return $true }
        Write-Host 'SelfSignedPinned requires CLI self-test (system-chain HTTP probe is not used as TrustAll).'
        return $false
    }

    # SystemTrust: must pass live + ready with normal HTTPS validation. No CLI-only escape hatch.
    $live = Test-HttpsHealthHttp -Port $Port -PublicHostname $PublicHostname -Path '/health/live'
    $ready = Test-HttpsHealthHttp -Port $Port -PublicHostname $PublicHostname -Path '/health/ready'
    if ($live -and $ready) {
        Write-Host "HTTPS health OK for hostname $PublicHostname on port $Port (SystemTrust)"
        return $true
    }

    Write-Host 'SystemTrust health requires /health/live and /health/ready with system TLS validation.'
    if ($cliOk) {
        Write-Host 'CLI self-test passed but HTTP health failed — treating as FAIL (no thumbprint-only escape).'
    }
    return $false
}

function Wait-HttpsHealthy {
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [int]$Port = 0,
        [string]$PublicHostname = 'localhost',
        [string]$InstallDir = '',
        [string]$CertificateMode = 'Store',
        [ValidateSet('SystemTrust', 'SelfSignedPinned', 'SelfSigned', 'Store', 'Pfx', '')]
        [string]$CertificateValidationMode = '',
        [int]$TimeoutSec = 60,
        [string]$BaseUrl = '',
        [switch]$RequireHttp
    )
    if ($Port -le 0 -and $BaseUrl -match ':(\d+)/?$') {
        $Port = [int]$Matches[1]
    }
    if ($Port -le 0) {
        throw 'Wait-HttpsHealthy requires -Port (or BaseUrl including port).'
    }
    if ([string]::IsNullOrWhiteSpace($PublicHostname) -and $BaseUrl -match '^https://([^/:]+)') {
        $PublicHostname = $Matches[1]
    }

    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    do {
        if (Test-HttpsHealthLocal -Port $Port -PublicHostname $PublicHostname -InstallDir $InstallDir `
                -CertificateMode $CertificateMode -CertificateValidationMode $CertificateValidationMode `
                -RequireHttp:$RequireHttp) {
            return $true
        }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)

    return (Test-HttpsHealthLocal -Port $Port -PublicHostname $PublicHostname -InstallDir $InstallDir `
            -CertificateMode $CertificateMode -CertificateValidationMode $CertificateValidationMode `
            -RequireHttp:$RequireHttp)
}

# -----------------------------------------------------------------------------
# Publish / runtime
# -----------------------------------------------------------------------------

function Get-PublishedBuildDir {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [string]$PublishDir = '',
        [switch]$NonInteractive
    )
    if ($PublishDir -and (Test-Path $PublishDir)) {
        return (Resolve-Path -LiteralPath $PublishDir).Path
    }

    $root = Get-RepoRoot
    $candidates = @(
        (Join-Path $root 'artifacts\publish'),
        (Join-Path $root 'artifacts\web'),
        (Join-Path $root 'artifacts\ControlPlane'),
        (Join-Path $root 'publish')
    )
    foreach ($c in $candidates) {
        $exe = Join-Path $c 'Nyxveil.ControlPlane.Web.exe'
        if (Test-Path $exe) {
            Write-Host "Auto-detected publish directory: $c"
            return $c
        }
    }

    if ($NonInteractive) {
        throw 'PublishDir not found. Pass -PublishDir or place build under artifacts/publish.'
    }

    $asked = Read-Host 'Path to published binaries (dotnet publish output)'
    if (-not (Test-Path $asked)) {
        throw "PublishDir not found: $asked"
    }
    return (Resolve-Path -LiteralPath $asked).Path
}

function Assert-DotNetAspNetRuntime {
    [CmdletBinding()]
    param(
        [int]$Major = 10
    )
    $dotnet = Get-Command dotnet -ErrorAction SilentlyContinue
    if (-not $dotnet) {
        throw @"
.NET SDK/runtime host (dotnet) was not found.
Install .NET $Major ASP.NET Core Runtime from:
https://dotnet.microsoft.com/download/dotnet/$Major.0
Then re-run this installer. The installer does not download runtimes automatically.
"@
    }

    $list = & dotnet --list-runtimes 2>$null
    $found = $false
    foreach ($line in $list) {
        if ($line -match ("^Microsoft\.AspNetCore\.App\s+{0}\." -f $Major)) {
            $found = $true
            break
        }
    }
    if (-not $found) {
        throw @"
.NET $Major ASP.NET Core Runtime (Microsoft.AspNetCore.App $Major.x) was not found.
Install it from: https://dotnet.microsoft.com/download/dotnet/$Major.0
(Select ASP.NET Core Runtime). The installer will not download packages automatically.
"@
    }
    Write-Host "Found Microsoft.AspNetCore.App $Major.x runtime."
}

# -----------------------------------------------------------------------------
# Certificates
# -----------------------------------------------------------------------------

function Get-CertificateFromStore {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Thumbprint,
        [string]$StoreName = 'My',
        [string]$StoreLocation = 'LocalMachine'
    )
    $tp = ($Thumbprint -replace '\s', '').ToUpperInvariant()
    $path = "Cert:\${StoreLocation}\${StoreName}"
    $cert = Get-ChildItem -Path $path -ErrorAction SilentlyContinue |
        Where-Object { $_.Thumbprint -eq $tp } |
        Select-Object -First 1
    return $cert
}

function Test-CertificateMatchesHostname {
    <#
    .SYNOPSIS
      RFC6125-ish: match PublicHostname against DNS SAN entries, else CN.
    #>
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory = $true)][System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate,
        [Parameter(Mandatory = $true)][string]$Hostname
    )
    $expected = $Hostname.Trim()
    if ($expected -match '^https?://') {
        try { $expected = ([Uri]$expected).Host } catch { }
    }
    $expected = $expected.TrimEnd('.').ToLowerInvariant()
    if ([string]::IsNullOrWhiteSpace($expected)) { return $false }

    $names = New-Object 'System.Collections.Generic.List[string]'
    foreach ($ext in $Certificate.Extensions) {
        if ($ext.Oid.Value -ne '2.5.29.17') { continue }
        $formatted = $ext.Format($true)
        foreach ($line in ($formatted -split '[\r\n]+')) {
            $t = $line.Trim()
            if ($t -match '(?i)^DNS\s*Name\s*[=:]?\s*(.+)$') {
                [void]$names.Add($Matches[1].Trim().TrimEnd('.').ToLowerInvariant())
            }
        }
    }

    if ($names.Count -eq 0) {
        try {
            $dns = $Certificate.GetNameInfo([System.Security.Cryptography.X509Certificates.X509NameType]::DnsName, $false)
            if (-not [string]::IsNullOrWhiteSpace($dns)) {
                [void]$names.Add($dns.Trim().TrimEnd('.').ToLowerInvariant())
            }
        }
        catch { }
        if ($names.Count -eq 0 -and $Certificate.Subject -match '(?i)CN\s*=\s*([^,]+)') {
            [void]$names.Add($Matches[1].Trim().Trim('"').TrimEnd('.').ToLowerInvariant())
        }
    }

    foreach ($n in $names) {
        if ($n -eq $expected) { return $true }
        if ($n.StartsWith('*.') -and $n.Length -gt 2) {
            $suffix = $n.Substring(1) # .example.com
            if ($expected.EndsWith($suffix) -and ($expected.Length -gt $suffix.Length)) {
                $left = $expected.Substring(0, $expected.Length - $suffix.Length)
                if ($left.IndexOf('.') -lt 0) { return $true }
            }
        }
    }
    return $false
}

function Assert-ValidServerCertificate {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate,
        [string]$PublicHostname = '',
        [ValidateSet('', 'SystemTrust', 'SelfSignedPinned')][string]$CertificateValidationMode = '',
        [string]$InstallDir = ''
    )
    if (-not $Certificate.HasPrivateKey) {
        throw "Certificate $($Certificate.Thumbprint) does not have an accessible private key."
    }
    $now = Get-Date
    if ($Certificate.NotAfter -lt $now) {
        throw "Certificate $($Certificate.Thumbprint) expired on $($Certificate.NotAfter.ToString('u'))."
    }
    if ($Certificate.NotBefore -gt $now) {
        throw "Certificate $($Certificate.Thumbprint) is not valid until $($Certificate.NotBefore.ToString('u'))."
    }

    $serverAuthOid = '1.3.6.1.5.5.7.3.1'
    $eku = $Certificate.EnhancedKeyUsageList
    if ($eku -and $eku.Count -gt 0) {
        $ok = $false
        foreach ($u in $eku) {
            if ($u.ObjectId -eq $serverAuthOid -or "$u" -match 'Server Authentication') {
                $ok = $true
                break
            }
        }
        if (-not $ok) {
            throw "Certificate $($Certificate.Thumbprint) Enhanced Key Usage does not include Server Authentication."
        }
    }

    if (-not [string]::IsNullOrWhiteSpace($PublicHostname)) {
        $hostnameOk = $false
        if (-not [string]::IsNullOrWhiteSpace($InstallDir)) {
            $exe = Join-Path $InstallDir 'Nyxveil.ControlPlane.Web.exe'
            if (Test-Path -LiteralPath $exe) {
                $cliArgs = @(
                    'certificate', 'validate',
                    '--hostname', $PublicHostname,
                    '--thumbprint', $Certificate.Thumbprint
                )
                if ($CertificateValidationMode -eq 'SelfSignedPinned') {
                    $cliArgs += '--self-signed-pinned'
                }
                try {
                    $cli = Invoke-NyxveilWebCli -InstallDir $InstallDir -Arguments $cliArgs -TimeoutMs 60000
                    if ($cli.ExitCode -eq 0) { $hostnameOk = $true }
                    else {
                        $msg = (($cli.StdErr, $cli.StdOut) | Where-Object { $_ } | Select-Object -First 1)
                        throw "certificate validate failed (exit $($cli.ExitCode)): $msg"
                    }
                }
                catch {
                    if ("$($_.Exception.Message)" -match 'certificate validate failed') { throw }
                    Write-Host "CLI certificate validate unavailable; using inline hostname match. ($($_.Exception.Message))"
                }
            }
        }
        if (-not $hostnameOk) {
            if (-not (Test-CertificateMatchesHostname -Certificate $Certificate -Hostname $PublicHostname)) {
                throw "Certificate $($Certificate.Thumbprint) does not match PublicHostname '$PublicHostname' (DNS SAN/CN)."
            }
        }
    }

    if ($CertificateValidationMode -eq 'SystemTrust') {
        $chain = New-Object System.Security.Cryptography.X509Certificates.X509Chain
        $chain.ChainPolicy.RevocationMode = [System.Security.Cryptography.X509Certificates.X509RevocationMode]::NoCheck
        if (-not $chain.Build($Certificate)) {
            $statuses = ($chain.ChainStatus | ForEach-Object { $_.Status.ToString() }) -join ', '
            throw "Certificate $($Certificate.Thumbprint) failed X509Chain.Build (SystemTrust): $statuses"
        }
    }
    # SelfSignedPinned: skip public chain; hostname + validity already enforced above.
}

function Import-NyxveilPfxCertificate {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$CertificatePath,
        [Parameter(Mandatory = $true)][securestring]$CertificatePassword,
        [string]$StoreName = 'My',
        [string]$StoreLocation = 'LocalMachine'
    )
    if (-not (Test-Path $CertificatePath)) {
        throw "PFX not found: $CertificatePath"
    }
    $storePath = "Cert:\${StoreLocation}\${StoreName}"
    $cert = Import-PfxCertificate -FilePath $CertificatePath -CertStoreLocation $storePath -Password $CertificatePassword -Exportable
    if ($cert -is [array]) { $cert = $cert | Select-Object -First 1 }
    Assert-ValidServerCertificate -Certificate $cert
    return $cert
}

function New-NyxveilSelfSignedCertificate {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$DnsName,
        [string]$StoreName = 'My',
        [string]$StoreLocation = 'LocalMachine'
    )
    Write-Warning 'Self-signed certificate is not recommended for public production deployment.'
    $storePath = "Cert:\${StoreLocation}\${StoreName}"
    $cert = New-SelfSignedCertificate `
        -DnsName $DnsName `
        -CertStoreLocation $storePath `
        -KeyExportPolicy Exportable `
        -KeySpec KeyExchange `
        -KeyLength 2048 `
        -HashAlgorithm SHA256 `
        -NotAfter (Get-Date).AddYears(2) `
        -TextExtension @('2.5.29.37={text}1.3.6.1.5.5.7.3.1')
    Assert-ValidServerCertificate -Certificate $cert -PublicHostname $DnsName -CertificateValidationMode SelfSignedPinned
    return $cert
}

function Grant-CertificatePrivateKeyAccess {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][System.Security.Cryptography.X509Certificates.X509Certificate2]$Certificate,
        [Parameter(Mandatory = $true)][string]$Account
    )
    $keyPath = $null
    $errors = New-Object 'System.Collections.Generic.List[string]'

    try {
        $rsa = $null
        try {
            $rsa = [System.Security.Cryptography.X509Certificates.RSACertificateExtensions]::GetRSAPrivateKey($Certificate)
        }
        catch {
            [void]$errors.Add("GetRSAPrivateKey: $($_.Exception.Message)")
        }
        if (-not $rsa) {
            try { $rsa = $Certificate.PrivateKey } catch { }
        }

        if ($rsa -is [System.Security.Cryptography.RSACryptoServiceProvider]) {
            $keyName = $rsa.CspKeyContainerInfo.UniqueKeyContainerName
            $machineKeyPath = Join-Path $env:ProgramData 'Microsoft\Crypto\RSA\MachineKeys'
            $candidate = Join-Path $machineKeyPath $keyName
            if (Test-Path -LiteralPath $candidate) { $keyPath = $candidate }
            else { [void]$errors.Add("RSA CSP container not found: $candidate") }
        }
        elseif ($rsa -and $rsa.GetType().FullName -match 'RSACng') {
            $keyName = $null
            try { $keyName = $rsa.Key.UniqueName } catch { [void]$errors.Add("RSACng UniqueName: $($_.Exception.Message)") }
            if ($keyName) {
                $machineKeyPath = Join-Path $env:ProgramData 'Microsoft\Crypto\Keys'
                $candidate = Join-Path $machineKeyPath $keyName
                if (Test-Path -LiteralPath $candidate) { $keyPath = $candidate }
                else { [void]$errors.Add("RSA CNG key file not found: $candidate") }
            }
        }
    }
    catch {
        [void]$errors.Add("RSA path: $($_.Exception.Message)")
    }

    if (-not $keyPath) {
        try {
            $ecdsa = [System.Security.Cryptography.X509Certificates.ECDsaCertificateExtensions]::GetECDsaPrivateKey($Certificate)
            if ($ecdsa -and $ecdsa.GetType().FullName -match 'ECDsaCng') {
                $keyName = $null
                try { $keyName = $ecdsa.Key.UniqueName } catch { [void]$errors.Add("ECDsaCng UniqueName: $($_.Exception.Message)") }
                if ($keyName) {
                    $machineKeyPath = Join-Path $env:ProgramData 'Microsoft\Crypto\Keys'
                    $candidate = Join-Path $machineKeyPath $keyName
                    if (Test-Path -LiteralPath $candidate) { $keyPath = $candidate }
                    else { [void]$errors.Add("ECDSA CNG key file not found: $candidate") }
                }
            }
            elseif (-not $ecdsa) {
                [void]$errors.Add('GetECDsaPrivateKey returned null.')
            }
        }
        catch {
            [void]$errors.Add("ECDSA path: $($_.Exception.Message)")
        }
    }

    if (-not $keyPath -or -not (Test-Path -LiteralPath $keyPath)) {
        $detail = ($errors -join '; ')
        throw "Cannot grant private-key ACL for certificate $($Certificate.Thumbprint) to '$Account' (key container not found). $detail"
    }

    try {
        $acl = Get-Acl -LiteralPath $keyPath
        $rule = New-Object System.Security.AccessControl.FileSystemAccessRule(
            $Account, 'Read', 'Allow')
        $acl.AddAccessRule($rule)
        Set-Acl -LiteralPath $keyPath -AclObject $acl
        Write-Host "Granted private-key Read to $Account on $keyPath."
    }
    catch {
        throw "Failed to grant certificate private key ACL to '$Account': $($_.Exception.Message)"
    }
}

# -----------------------------------------------------------------------------
# ACL / service helpers
# -----------------------------------------------------------------------------

function Initialize-NyxveilRestrictedDirectory {
    <#
    .SYNOPSIS
      Creates a directory early with SYSTEM + Administrators only (inheritance disabled).
      Call BEFORE writing secrets/keys. Service account is added later via Set-NyxveilDirectoryAcls.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Path
    )
    if (-not (Test-Path -LiteralPath $Path)) {
        New-Item -ItemType Directory -Force -Path $Path | Out-Null
    }

    # Disable inheritance and grant only SYSTEM + Administrators (FullControl).
    Invoke-NativeChecked -Name "icacls reset $Path" -Script {
        & icacls $Path /inheritance:r /C | Out-Null
    }
    Invoke-NativeChecked -Name "icacls SYSTEM $Path" -Script {
        & icacls $Path /grant:r "NT AUTHORITY\SYSTEM:(OI)(CI)F" /C | Out-Null
    }
    Invoke-NativeChecked -Name "icacls Administrators $Path" -Script {
        & icacls $Path /grant:r "BUILTIN\Administrators:(OI)(CI)F" /C | Out-Null
    }
    foreach ($principal in @(
            'BUILTIN\Users',
            'Everyone',
            'NT AUTHORITY\Authenticated Users'
        )) {
        # Best-effort removal; ignore if ACE absent.
        & icacls $Path /remove:g $principal /T /C 2>$null | Out-Null
        $global:LASTEXITCODE = 0
    }
    Write-Host "Restricted directory ready (SYSTEM+Administrators only): $Path"
}

function Set-NyxveilDirectoryAcls {
    <#
    .SYNOPSIS
      Grants the service account minimal rights after Service SID exists.
      Sensitive dirs (secrets, keys, data-protection) stay inheritance-disabled without Users/Everyone.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$InstallDir,
        [Parameter(Mandatory = $true)][string]$DataDir,
        [Parameter(Mandatory = $true)][string]$LogsDir,
        [Parameter(Mandatory = $true)][string]$SecretsDir,
        [Parameter(Mandatory = $true)][string]$ServiceAccount,
        [string]$KeysDir = '',
        [string]$DataProtectionDir = ''
    )
    if ([string]::IsNullOrWhiteSpace($DataProtectionDir)) {
        $DataProtectionDir = Join-Path (Get-ProgramDataRoot) 'data-protection'
    }

    # Call AFTER New-NyxveilWindowsService / Ensure-NyxveilServiceSid so NT SERVICE\* SID exists for icacls.
    foreach ($d in @($InstallDir, $DataDir, $LogsDir)) {
        if ($d -and -not (Test-Path $d)) {
            New-Item -ItemType Directory -Force -Path $d | Out-Null
        }
    }
    foreach ($sensitive in @($SecretsDir, $KeysDir, $DataProtectionDir)) {
        if ($sensitive) {
            Initialize-NyxveilRestrictedDirectory -Path $sensitive
        }
    }

    Invoke-NativeChecked -Name "icacls InstallDir RX" -Script {
        & icacls $InstallDir /grant "${ServiceAccount}:(OI)(CI)RX" /T /C | Out-Null
    }
    foreach ($rw in @($DataDir, $LogsDir)) {
        if ($rw) {
            Invoke-NativeChecked -Name "icacls $rw M" -Script {
                & icacls $rw /grant "${ServiceAccount}:(OI)(CI)M" /T /C | Out-Null
            }
        }
    }
    # Sensitive: Modify for service only (dirs already SYSTEM+Administrators).
    foreach ($sensitive in @($SecretsDir, $KeysDir, $DataProtectionDir)) {
        if ($sensitive) {
            Invoke-NativeChecked -Name "icacls $sensitive service M" -Script {
                & icacls $sensitive /grant "${ServiceAccount}:(OI)(CI)M" /T /C | Out-Null
            }
            foreach ($principal in @(
                    'BUILTIN\Users',
                    'Everyone',
                    'NT AUTHORITY\Authenticated Users'
                )) {
                & icacls $sensitive /remove:g $principal /T /C 2>$null | Out-Null
                $global:LASTEXITCODE = 0
            }
        }
    }
}

function Test-LocalSqlServerService {
    [CmdletBinding()]
    [OutputType([bool])]
    param()
    $svc = Get-Service -Name 'MSSQLSERVER' -ErrorAction SilentlyContinue
    return [bool]$svc
}

function New-NyxveilWindowsService {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$ServiceName,
        [Parameter(Mandatory = $true)][string]$ExePath,
        [Parameter(Mandatory = $true)][string]$ServiceAccount,
        [switch]$DependOnLocalSql
    )
    if (-not (Test-Path $ExePath)) {
        throw "Service binary not found: $ExePath"
    }

    $existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    $binPath = "`"$ExePath`""

    if ($existing) {
        Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
        Invoke-NativeChecked -Name "sc.exe config binPath $ServiceName" -Script {
            & sc.exe config $ServiceName binPath= $binPath | Out-Null
        }
    }
    else {
        # Virtual account: obj= "NT SERVICE\ServiceName"
        # Created STOPPED (start= delayed-auto does not start now).
        Invoke-NativeChecked -Name "sc.exe create $ServiceName" -Script {
            & sc.exe create $ServiceName `
                binPath= $binPath `
                DisplayName= 'Nyxveil Control Plane' `
                start= delayed-auto `
                obj= $ServiceAccount | Out-Null
        }
    }

    Invoke-NativeChecked -Name "sc.exe config start $ServiceName" -Script {
        & sc.exe config $ServiceName start= delayed-auto | Out-Null
    }
    Invoke-NativeChecked -Name "sc.exe failure $ServiceName" -Script {
        & sc.exe failure $ServiceName reset= 86400 actions= restart/5000/restart/30000/restart/60000 | Out-Null
    }
    Invoke-NativeChecked -Name "sc.exe failureflag $ServiceName" -Script {
        & sc.exe failureflag $ServiceName 1 | Out-Null
    }

    if ($DependOnLocalSql -and (Test-LocalSqlServerService)) {
        Invoke-NativeChecked -Name "sc.exe config depend MSSQLSERVER" -Script {
            & sc.exe config $ServiceName depend= MSSQLSERVER | Out-Null
        }
        Write-Host 'Service dependency set: MSSQLSERVER (local SQL detected).'
    }
    else {
        Invoke-NativeChecked -Name "sc.exe config depend clear" -Script {
            & sc.exe config $ServiceName depend= '' | Out-Null
        }
    }

    # Leave stopped — caller starts after SID/ACL/SQL grants.
    Stop-Service -Name $ServiceName -Force -ErrorAction SilentlyContinue
}

function Ensure-NyxveilServiceSid {
    <#
    .SYNOPSIS
      For virtual accounts: sc.exe sidtype unrestricted, qsidtype, resolve account to SID.
      Fails closed if SID cannot be resolved.
    #>
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory = $true)][string]$ServiceName,
        [string]$ServiceAccount = ''
    )
    if ([string]::IsNullOrWhiteSpace($ServiceAccount)) {
        $ServiceAccount = "NT SERVICE\$ServiceName"
    }

    if ($ServiceAccount -like 'NT SERVICE\*') {
        Invoke-NativeChecked -Name "sc.exe sidtype $ServiceName unrestricted" -Script {
            & sc.exe sidtype $ServiceName unrestricted | Out-Null
        }
        Invoke-NativeChecked -Name "sc.exe qsidtype $ServiceName" -Script {
            & sc.exe qsidtype $ServiceName | Out-Null
        }
    }

    try {
        $nt = New-Object System.Security.Principal.NTAccount($ServiceAccount)
        $sid = $nt.Translate([System.Security.Principal.SecurityIdentifier])
        if (-not $sid -or [string]::IsNullOrWhiteSpace($sid.Value)) {
            throw 'Translate returned empty SID.'
        }
        Write-Host "Service SID resolved: $ServiceAccount -> $($sid.Value)"
        return [string]$sid.Value
    }
    catch {
        throw "Failed to resolve service account '$ServiceAccount' to a SID (required before ACL/SQL grants): $($_.Exception.Message)"
    }
}

function Set-NyxveilServiceEnvironment {
    <#
    .SYNOPSIS
      Sets service-specific Environment REG_MULTI_SZ (not machine-wide).
      .NET Windows Service reads HKLM\...\Services\<Name>\Environment.
    #>
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$ServiceName,
        [Parameter(Mandatory = $true)][string]$InstallDir
    )
    $keyPath = "HKLM:\SYSTEM\CurrentControlSet\Services\$ServiceName"
    if (-not (Test-Path -LiteralPath $keyPath)) {
        throw "Service registry key not found: $keyPath (create the Windows service first)."
    }

    $envValues = @(
        'ASPNETCORE_ENVIRONMENT=Production',
        'DOTNET_ENVIRONMENT=Production',
        "ASPNETCORE_CONTENTROOT=$InstallDir"
    )

    Invoke-NativeChecked -Name "Set service Environment ($ServiceName)" -Script {
        $item = Get-ItemProperty -LiteralPath $keyPath -Name Environment -ErrorAction SilentlyContinue
        if ($null -eq $item -or $null -eq $item.Environment) {
            New-ItemProperty -LiteralPath $keyPath -Name Environment -PropertyType MultiString -Value $envValues -Force | Out-Null
        }
        else {
            Set-ItemProperty -LiteralPath $keyPath -Name Environment -Value $envValues
        }
    }
    Write-Host "Service environment set (ASPNETCORE/DOTNET=Production; CONTENTROOT=$InstallDir)."
}

function Grant-SqlLoginForServiceAccount {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Server,
        [Parameter(Mandatory = $true)][string]$DatabaseName,
        [Parameter(Mandatory = $true)][string]$ServiceAccount,
        [ValidateSet('Windows', 'Sql')][string]$DatabaseAuth = 'Windows',
        [string]$DatabaseUser = '',
        [securestring]$DatabasePassword
    )
    if ($DatabaseAuth -ne 'Windows') {
        Write-Host 'SQL Auth mode: skipping Windows service login grant.'
        return
    }

    # db_owner on the application database only (simplifies EF migrations under the service).
    # Split runtime vs migration roles later if desired (db_datareader/writer + ddladmin).
    $login = $ServiceAccount.Replace("'", "''")
    $db = $DatabaseName.Replace("'", "''")
    $sql = @"
IF NOT EXISTS (SELECT 1 FROM sys.server_principals WHERE name = N'$login')
BEGIN
    CREATE LOGIN [$ServiceAccount] FROM WINDOWS;
END
USE [$DatabaseName];
IF NOT EXISTS (SELECT 1 FROM sys.database_principals WHERE name = N'$login')
BEGIN
    CREATE USER [$ServiceAccount] FOR LOGIN [$ServiceAccount];
END
-- App DB only: db_owner for runtime + migrations simplicity (not server sysadmin).
IF IS_ROLEMEMBER('db_owner', N'$login') = 0 OR IS_ROLEMEMBER('db_owner', N'$login') IS NULL
BEGIN
    ALTER ROLE db_owner ADD MEMBER [$ServiceAccount];
END
"@
    Invoke-SqlCmdFailClosed -Server $Server -Query $sql -DatabaseName $DatabaseName `
        -DatabaseAuth $DatabaseAuth -DatabaseUser $DatabaseUser -DatabasePassword $DatabasePassword
}

function Build-PublicBaseUrl {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory = $true)][string]$PublicHostname,
        [Parameter(Mandatory = $true)][int]$Port
    )
    $hostName = $PublicHostname.Trim()
    if ($hostName -match '^https?://') {
        $uri = [Uri]$hostName
        $hostName = $uri.Host
    }
    if ($Port -eq 443) {
        return "https://$hostName"
    }
    return "https://${hostName}:${Port}"
}

function Write-AppsettingsProduction {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$InstallDir,
        [Parameter(Mandatory = $true)][int]$Port,
        [Parameter(Mandatory = $true)][string]$BindAddress,
        [Parameter(Mandatory = $true)][string]$PublicHostname,
        [Parameter(Mandatory = $true)][string]$PublicBaseUrl,
        [Parameter(Mandatory = $true)][string]$CertificateMode,
        [Parameter(Mandatory = $true)][string]$CertificateThumbprint,
        [Parameter(Mandatory = $true)][string]$DatabaseServer,
        [Parameter(Mandatory = $true)][string]$DatabaseName,
        [Parameter(Mandatory = $true)][ValidateSet('Windows', 'Sql')][string]$DatabaseAuth,
        [string]$DatabaseUser = '',
        [string]$SecretsDir = '',
        [string]$KeysDir = '',
        [ValidateSet('SystemTrust', 'SelfSignedPinned')][string]$CertificateValidationMode = 'SystemTrust',
        [bool]$TrustSqlServerCertificate = $false
    )
    if ([string]::IsNullOrWhiteSpace($SecretsDir)) {
        $SecretsDir = Join-Path (Get-ProgramDataRoot) 'secrets'
    }
    if ([string]::IsNullOrWhiteSpace($KeysDir)) {
        $KeysDir = Join-Path (Get-ProgramDataRoot) 'keys'
    }

    Assert-ValidDatabaseName -DatabaseName $DatabaseName
    Assert-ValidPort -Port $Port

    if ([string]::IsNullOrWhiteSpace($CertificateThumbprint)) {
        throw 'Write-AppsettingsProduction requires CertificateThumbprint (production runtime uses Certificate:Mode=Store).'
    }

    # Production runtime ALWAYS uses Windows Certificate Store + thumbprint after import.
    # Never leave Mode=Pfx with path, or Mode=SelfSigned without thumbprint, for production.
    # ValidationMode is separate from load Mode (Store).
    $runtimeCertMode = 'Store'
    if ($CertificateMode -notin @('Store', 'Pfx', 'SelfSigned')) {
        throw "Unsupported CertificateMode '$CertificateMode'."
    }

    $trustLiteral = if ($TrustSqlServerCertificate) { 'True' } else { 'False' }
    if ($DatabaseAuth -eq 'Windows') {
        $cs = "Server=$DatabaseServer;Database=$DatabaseName;Trusted_Connection=True;TrustServerCertificate=$trustLiteral;Encrypt=True"
    }
    else {
        # Password supplied via DPAPI protected secrets provider (sql-password.dpapi) — never inline.
        $cs = "Server=$DatabaseServer;Database=$DatabaseName;User ID=$DatabaseUser;TrustServerCertificate=$trustLiteral;Encrypt=True"
    }

    # Hosting is SoT for listen bind (Program.cs ConfigureKestrelHttps). Do not emit Kestrel:Endpoints.
    $settings = [ordered]@{
        ConnectionStrings = [ordered]@{
            ControlPlane = $cs
        }
        Database = [ordered]@{
            Encrypt                   = $true
            TrustSqlServerCertificate = [bool]$TrustSqlServerCertificate
        }
        Hosting = [ordered]@{
            BindAddress    = $BindAddress
            Port           = $Port
            PublicHostname = $PublicHostname
            PublicBaseUrl  = $PublicBaseUrl
        }
        Certificate = [ordered]@{
            Mode                     = $runtimeCertMode
            ValidationMode           = $CertificateValidationMode
            Thumbprint               = $CertificateThumbprint
            StoreName                = 'My'
            StoreLocation            = 'LocalMachine'
            PfxPath                  = ''
            PfxPasswordProtectedPath = ''
        }
        Https = [ordered]@{
            RequireHttpsInProduction = $true
        }
        Security = [ordered]@{
            # LicenseKekHex loaded from DPAPI secrets (license-kek.dpapi) — never plaintext here.
        }
        Signing = [ordered]@{
            Issuer            = 'nyxveil-control-plane'
            Audience          = 'nvp-node'
            KeyProtectionPath = $KeysDir
        }
        ProtectedSecrets = [ordered]@{
            Path = $SecretsDir
        }
        Setup = [ordered]@{
            AllowWebBootstrap = $false
        }
        Logging = [ordered]@{
            LogLevel = [ordered]@{
                Default                = 'Information'
                'Microsoft.AspNetCore' = 'Warning'
            }
        }
        AllowedHosts = '*'
    }

    $path = Join-Path $InstallDir 'appsettings.Production.json'
    ($settings | ConvertTo-Json -Depth 10) | Set-Content -LiteralPath $path -Encoding UTF8
    return $path
}

function Invoke-AdminCreate {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$InstallDir,
        [Parameter(Mandatory = $true)][string]$AdminUser,
        [Parameter(Mandatory = $true)][securestring]$AdminPassword
    )
    $exe = Join-Path $InstallDir 'Nyxveil.ControlPlane.Web.exe'
    if (-not (Test-Path $exe)) {
        throw "Web host not found: $exe"
    }

    $plain = ConvertFrom-SecureStringPlain -SecureString $AdminPassword
    try {
        $psi = New-Object System.Diagnostics.ProcessStartInfo
        $psi.FileName = $exe
        $psi.Arguments = "admin create --username `"$AdminUser`" --stdin"
        $psi.WorkingDirectory = $InstallDir
        $psi.UseShellExecute = $false
        $psi.RedirectStandardInput = $true
        $psi.RedirectStandardOutput = $true
        $psi.RedirectStandardError = $true
        $psi.CreateNoWindow = $true
        $psi.EnvironmentVariables['ASPNETCORE_ENVIRONMENT'] = 'Production'

        $p = New-Object System.Diagnostics.Process
        $p.StartInfo = $psi
        [void]$p.Start()
        $p.StandardInput.WriteLine($plain)
        $p.StandardInput.Close()
        $stdout = $p.StandardOutput.ReadToEnd()
        $stderr = $p.StandardError.ReadToEnd()
        $p.WaitForExit(120000) | Out-Null

        if ($p.ExitCode -ne 0) {
            if ($stdout) { Write-Host $stdout }
            if ($stderr) { Write-Host $stderr }
            throw "admin create failed with exit code $($p.ExitCode)."
        }
        Write-Host "First admin '$AdminUser' created."
    }
    finally {
        $plain = $null
    }
}

function Get-CertificateSummary {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Thumbprint
    )
    $cert = Get-CertificateFromStore -Thumbprint $Thumbprint
    if (-not $cert) { return $null }
    [pscustomobject]@{
        Subject    = $cert.Subject
        Thumbprint = $cert.Thumbprint
        NotBefore  = $cert.NotBefore
        NotAfter   = $cert.NotAfter
        DaysLeft   = [int]($cert.NotAfter - (Get-Date)).TotalDays
    }
}

function Backup-DirectoryContents {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$SourceDir,
        [Parameter(Mandatory = $true)][string]$DestinationDir
    )
    if (-not (Test-Path $DestinationDir)) {
        New-Item -ItemType Directory -Force -Path $DestinationDir | Out-Null
    }
    Copy-Item -Path (Join-Path $SourceDir '*') -Destination $DestinationDir -Recurse -Force
}

function Protect-BytesAesPassword {
    [CmdletBinding()]
    [OutputType([byte[]])]
    param(
        [Parameter(Mandatory = $true)][byte[]]$PlainBytes,
        [Parameter(Mandatory = $true)][securestring]$Password
    )
    $plainPass = ConvertFrom-SecureStringPlain -SecureString $Password
    try {
        $salt = New-Object byte[] 16
        $nonce = New-Object byte[] 12
        $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
        $rng.GetBytes($salt)
        $rng.GetBytes($nonce)
        $rng.Dispose()

        $derive = New-Object System.Security.Cryptography.Rfc2898DeriveBytes($plainPass, $salt, 100000)
        $key = $derive.GetBytes(32)
        $derive.Dispose()

        # AES-CBC + HMAC-SHA256 (PS 5.1 compatible; header: NVXK1 | salt | iv | tag | cipher)
        $iv = New-Object byte[] 16
        [Array]::Copy($nonce, 0, $iv, 0, 12)

        $aes = [System.Security.Cryptography.Aes]::Create()
        $aes.Key = $key
        $aes.IV = $iv
        $aes.Mode = [System.Security.Cryptography.CipherMode]::CBC
        $aes.Padding = [System.Security.Cryptography.PaddingMode]::PKCS7
        $enc = $aes.CreateEncryptor()
        $cipher = $enc.TransformFinalBlock($PlainBytes, 0, $PlainBytes.Length)
        $enc.Dispose()
        $aes.Dispose()

        # Derive separate MAC key
        $derive2 = New-Object System.Security.Cryptography.Rfc2898DeriveBytes($plainPass, $salt, 100001)
        $hmacKey = $derive2.GetBytes(32)
        $derive2.Dispose()

        $hmac = New-Object System.Security.Cryptography.HMACSHA256
        $hmac.Key = $hmacKey
        $toMac = New-Object byte[] (16 + 16 + $cipher.Length)
        [Array]::Copy($salt, 0, $toMac, 0, 16)
        [Array]::Copy($iv, 0, $toMac, 16, 16)
        [Array]::Copy($cipher, 0, $toMac, 32, $cipher.Length)
        $tag = $hmac.ComputeHash($toMac)
        $hmac.Dispose()
        [Array]::Clear($key, 0, $key.Length)
        [Array]::Clear($hmacKey, 0, $hmacKey.Length)

        $magic = [Text.Encoding]::ASCII.GetBytes('NVXK1')
        $out = New-Object byte[] ($magic.Length + 16 + 16 + 32 + $cipher.Length)
        [Array]::Copy($magic, 0, $out, 0, 5)
        [Array]::Copy($salt, 0, $out, 5, 16)
        [Array]::Copy($iv, 0, $out, 21, 16)
        [Array]::Copy($tag, 0, $out, 37, 32)
        [Array]::Copy($cipher, 0, $out, 69, $cipher.Length)
        return $out
    }
    finally {
        $plainPass = $null
    }
}

function Unprotect-BytesAesPassword {
    [CmdletBinding()]
    [OutputType([byte[]])]
    param(
        [Parameter(Mandatory = $true)][byte[]]$ProtectedBytes,
        [Parameter(Mandatory = $true)][securestring]$Password
    )
    $plainPass = ConvertFrom-SecureStringPlain -SecureString $Password
    try {
        $magic = [Text.Encoding]::ASCII.GetString($ProtectedBytes, 0, 5)
        if ($magic -ne 'NVXK1') { throw 'Invalid signing-key backup format (expected NVXK1).' }
        $salt = New-Object byte[] 16
        $iv = New-Object byte[] 16
        $tag = New-Object byte[] 32
        [Array]::Copy($ProtectedBytes, 5, $salt, 0, 16)
        [Array]::Copy($ProtectedBytes, 21, $iv, 0, 16)
        [Array]::Copy($ProtectedBytes, 37, $tag, 0, 32)
        $cipherLen = $ProtectedBytes.Length - 69
        $cipher = New-Object byte[] $cipherLen
        [Array]::Copy($ProtectedBytes, 69, $cipher, 0, $cipherLen)

        $derive2 = New-Object System.Security.Cryptography.Rfc2898DeriveBytes($plainPass, $salt, 100001)
        $hmacKey = $derive2.GetBytes(32)
        $derive2.Dispose()
        $hmac = New-Object System.Security.Cryptography.HMACSHA256
        $hmac.Key = $hmacKey
        $toMac = New-Object byte[] (16 + 16 + $cipherLen)
        [Array]::Copy($salt, 0, $toMac, 0, 16)
        [Array]::Copy($iv, 0, $toMac, 16, 16)
        [Array]::Copy($cipher, 0, $toMac, 32, $cipherLen)
        $actual = $hmac.ComputeHash($toMac)
        $hmac.Dispose()
        $ok = $true
        for ($i = 0; $i -lt 32; $i++) {
            if ($actual[$i] -ne $tag[$i]) { $ok = $false }
        }
        [Array]::Clear($hmacKey, 0, $hmacKey.Length)
        if (-not $ok) { throw 'Signing-key backup password invalid or file corrupted (MAC mismatch).' }

        $derive = New-Object System.Security.Cryptography.Rfc2898DeriveBytes($plainPass, $salt, 100000)
        $key = $derive.GetBytes(32)
        $derive.Dispose()
        $aes = [System.Security.Cryptography.Aes]::Create()
        $aes.Key = $key
        $aes.IV = $iv
        $aes.Mode = [System.Security.Cryptography.CipherMode]::CBC
        $aes.Padding = [System.Security.Cryptography.PaddingMode]::PKCS7
        $dec = $aes.CreateDecryptor()
        $plain = $dec.TransformFinalBlock($cipher, 0, $cipher.Length)
        $dec.Dispose()
        $aes.Dispose()
        [Array]::Clear($key, 0, $key.Length)
        return $plain
    }
    finally {
        $plainPass = $null
    }
}

Export-ModuleMember -Function @(
    'Assert-Administrator',
    'Get-RepoRoot',
    'Get-ProgramDataRoot',
    'Get-DefaultPaths',
    'Get-OperationalConfigPath',
    'New-OperationalConfigObject',
    'Read-OperationalConfig',
    'Write-OperationalConfig',
    'Assert-ValidPort',
    'Get-PortOccupancyDetails',
    'Test-PortOwnedByService',
    'Test-TcpPortAvailable',
    'Get-NyxveilFirewallRuleName',
    'New-NyxveilFirewallRule',
    'Update-NyxveilFirewallRule',
    'Remove-NyxveilFirewallRule',
    'Protect-SecretString',
    'Unprotect-SecretString',
    'Write-ProtectedSecret',
    'Read-ProtectedSecret',
    'New-LicenseKekHex',
    'ConvertFrom-SecureStringPlain',
    'Test-SqlCmdAvailable',
    'Assert-SqlCmdAvailable',
    'Assert-ValidDatabaseName',
    'Test-IsLocalDatabaseServer',
    'Test-RemoteWindowsAuthSupported',
    'Resolve-TrustSqlServerCertificatePolicy',
    'Get-NyxveilDatabaseSettings',
    'Invoke-NativeChecked',
    'Get-NyxveilSqlcmdArgs',
    'Invoke-NyxveilSql',
    'Invoke-SqlCmdFailClosed',
    'New-CreateDatabaseScriptCopy',
    'Get-HealthTarget',
    'Get-LocalHealthBaseUrl',
    'Get-NyxveilWebExePath',
    'Invoke-NyxveilWebCli',
    'Invoke-NyxveilSelfTestCli',
    'Test-HttpsHealthHttp',
    'Test-HttpsHealth',
    'Test-HttpsHealthLocal',
    'Wait-HttpsHealthy',
    'Get-PublishedBuildDir',
    'Assert-DotNetAspNetRuntime',
    'Get-CertificateFromStore',
    'Test-CertificateMatchesHostname',
    'Assert-ValidServerCertificate',
    'Import-NyxveilPfxCertificate',
    'New-NyxveilSelfSignedCertificate',
    'Grant-CertificatePrivateKeyAccess',
    'Initialize-NyxveilRestrictedDirectory',
    'Set-NyxveilDirectoryAcls',
    'Test-LocalSqlServerService',
    'New-NyxveilWindowsService',
    'Ensure-NyxveilServiceSid',
    'Set-NyxveilServiceEnvironment',
    'Grant-SqlLoginForServiceAccount',
    'Build-PublicBaseUrl',
    'Write-AppsettingsProduction',
    'Invoke-AdminCreate',
    'Get-CertificateSummary',
    'Backup-DirectoryContents',
    'Protect-BytesAesPassword',
    'Unprotect-BytesAesPassword'
)
