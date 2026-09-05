#Requires -Version 5.1
<#
.SYNOPSIS
  Exercise deployment ACL helpers on disposable Windows directories, without a service or SQL.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$ScratchDir
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force

$scratchRoot = (Resolve-Path -LiteralPath $ScratchDir).Path.TrimEnd('\')
$testRoot = Join-Path $scratchRoot ('nyxveil-acl-test-' + [Guid]::NewGuid().ToString('N'))
$ownerSid = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$broadSids = @('S-1-5-32-545', 'S-1-1-0', 'S-1-5-11')
$allow = [Security.AccessControl.AccessControlType]::Allow

function Assert-Acl([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}

function Get-SidRules([string]$Path) {
    return (Get-Acl -LiteralPath $Path).GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier])
}

function Assert-Granted([string]$Path, [string]$Sid, [Security.AccessControl.FileSystemRights]$Rights) {
    $matching = @(Get-SidRules $Path | Where-Object {
        $_.IdentityReference.Value -eq $Sid -and $_.AccessControlType -eq $allow -and
        ($_.FileSystemRights -band $Rights) -eq $Rights
    })
    Assert-Acl ($matching.Count -gt 0) "Missing $Rights for $Sid on $Path"
}

try {
    $null = New-Item -ItemType Directory -Path $testRoot
    $paths = @{}
    foreach ($name in @('install', 'data', 'logs', 'secrets', 'keys', 'data-protection')) {
        $path = Join-Path $testRoot $name
        $paths[$name] = $path
        $null = New-Item -ItemType Directory -Path $path
        # Keep the test owner able to inspect and clean up on non-elevated hosts.
        Invoke-NativeChecked -Name 'Seed test owner ACL' -Script {
            & icacls $path /grant:r "*${ownerSid}:(OI)(CI)F" | Out-Null
        }
    }

    foreach ($name in @('secrets', 'keys', 'data-protection')) {
        $path = $paths[$name]
        $child = Join-Path $path 'probe.txt'
        Set-Content -LiteralPath $child -Value 'Not a secret.'
        foreach ($broadSid in $broadSids) {
            Invoke-NativeChecked -Name 'Seed broad directory ACE' -Script {
                & icacls $path /grant "*${broadSid}:(OI)(CI)R" | Out-Null
            }
            Invoke-NativeChecked -Name 'Seed broad child ACE' -Script {
                & icacls $child /grant "*${broadSid}:R" | Out-Null
            }
        }
        Initialize-NyxveilRestrictedDirectory -Path $path
    }

    # LocalService is an existing identity; this does not create or modify a service.
    foreach ($run in 1..2) {
        Set-NyxveilDirectoryAcls -InstallDir $paths['install'] -DataDir $paths['data'] `
            -LogsDir $paths['logs'] -SecretsDir $paths['secrets'] -KeysDir $paths['keys'] `
            -DataProtectionDir $paths['data-protection'] -ServiceAccount '*S-1-5-19'

        foreach ($name in @('secrets', 'keys', 'data-protection')) {
            $path = $paths[$name]
            Assert-Acl (Get-Acl -LiteralPath $path).AreAccessRulesProtected "Inheritance enabled on $path"
            Assert-Granted $path 'S-1-5-18' ([Security.AccessControl.FileSystemRights]::FullControl)
            Assert-Granted $path 'S-1-5-32-544' ([Security.AccessControl.FileSystemRights]::FullControl)
            Assert-Granted $path 'S-1-5-19' ([Security.AccessControl.FileSystemRights]::Modify)
            foreach ($target in @($path, (Join-Path $path 'probe.txt'))) {
                $broad = @(Get-SidRules $target | Where-Object {
                    $_.IdentityReference.Value -in $broadSids -and $_.AccessControlType -eq $allow
                })
                Assert-Acl ($broad.Count -eq 0) "Broad access remains on $target"
            }
        }
        Assert-Granted $paths['install'] 'S-1-5-19' ([Security.AccessControl.FileSystemRights]::ReadAndExecute)
        Assert-Granted $paths['data'] 'S-1-5-19' ([Security.AccessControl.FileSystemRights]::Modify)
        Assert-Granted $paths['logs'] 'S-1-5-19' ([Security.AccessControl.FileSystemRights]::Modify)
        Write-Host "[PASS] Real Windows ACLs: SID grants, recursive broad-access removal, run $run."
    }
}
finally {
    if (Test-Path -LiteralPath $testRoot) {
        $resolvedTest = (Resolve-Path -LiteralPath $testRoot).Path
        if ((Split-Path -Parent $resolvedTest) -ne $scratchRoot -or
            (Split-Path -Leaf $resolvedTest) -notlike 'nyxveil-acl-test-*') {
            throw "Refusing cleanup outside scratch directory: $resolvedTest"
        }
        Remove-Item -LiteralPath $resolvedTest -Recurse -Force
    }
}
