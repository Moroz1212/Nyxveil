#Requires -Version 5.1
<#
.SYNOPSIS
  Management API interop: Go signs nvp-node-req-v2 vectors, C# verifier accepts them.
#>
[CmdletBinding()]
param()
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$root = Split-Path -Parent $PSScriptRoot
$vector = Join-Path ([IO.Path]::GetTempPath()) ('nyxveil-req-v2-' + [guid]::NewGuid().ToString('N') + '.json')
try {
    & go run (Join-Path $root 'tests\ManagementInterop\sign.go') $vector
    if ($LASTEXITCODE -ne 0) { throw 'Go management signing failed' }
    & dotnet run --project (Join-Path $root 'tests\CoreInterop\CsEmit\CsEmit.csproj') -c Release -- verify-management-request --vectors $vector
    if ($LASTEXITCODE -ne 0) { throw 'C# management verification failed' }
    Write-Host 'MANAGEMENT_REQ_V2_GO_TO_CS=PASS'
}
finally {
    if (Test-Path -LiteralPath $vector) { Remove-Item -LiteralPath $vector -Force }
}
