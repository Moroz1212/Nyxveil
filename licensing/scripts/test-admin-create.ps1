#Requires -Version 5.1
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
$module = Import-Module (Join-Path $PSScriptRoot 'Nyxveil.ControlPlane.Deploy.psm1') -Force -PassThru
& $module {
    $original = (Get-Item Function:Invoke-NyxveilWebCli).ScriptBlock
    try {
        function Invoke-NyxveilWebCli {
            param($InstallDir, $Arguments, $StdinSecure)
            if ($StdinSecure -isnot [securestring] -or $Arguments -contains 'test-only') {
                throw 'Password must be supplied separately as SecureString.'
            }
            return [pscustomobject]@{ ExitCode = $script:MockAdminExit; StdOut = ''; StdErr = '' }
        }
        $password = ConvertTo-SecureString 'test-only' -AsPlainText -Force
        foreach ($allow in @($false, $true)) {
            foreach ($exitCode in @(0, 1, 2, 3)) {
                $script:MockAdminExit = $exitCode
                $failed = $false
                try {
                    Invoke-AdminCreate -InstallDir 'unused-test-path' -AdminUser 'test@example.invalid' `
                        -AdminPassword $password -AllowAlreadyExists:$allow
                }
                catch { $failed = $true }
                $expectedFailure = $exitCode -ne 0 -and -not ($allow -and $exitCode -eq 2)
                if ($failed -ne $expectedFailure) { throw "Wrong result: exit=$exitCode AllowAlreadyExists=$allow" }
            }
        }
        Write-Host '[PASS] Admin CLI: success, existing admin, invalid password and other failures in Fresh/Repair.'
    }
    finally {
        Set-Item Function:Invoke-NyxveilWebCli -Value $original
        Remove-Variable MockAdminExit -Scope Script -ErrorAction SilentlyContinue
    }
}
