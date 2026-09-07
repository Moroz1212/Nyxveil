#Requires -Version 5.1
<#
.SYNOPSIS
  Launch Release Nyxveil GUI with --visual-qa + --screenshot (WPF RenderTargetBitmap).
#>
$ErrorActionPreference = "Stop"

$WinRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
$Exe = Join-Path $WinRoot "gui\src\Nyxveil.App\bin\Release\net10.0-windows\win-x64\Nyxveil.exe"
$QaDir = Join-Path $WinRoot "dist\reference-qa"
New-Item -ItemType Directory -Force -Path $QaDir | Out-Null

function Shot([string]$Mode, [string]$FileName) {
  Get-Process -Name "Nyxveil" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
  Start-Sleep -Milliseconds 400
  $out = Join-Path $QaDir $FileName
  if (Test-Path $out) { Remove-Item $out -Force }
  $args = @(
    ("--visual-qa=" + $Mode),
    ("--screenshot=" + $out)
  )
  $p = Start-Process -FilePath $Exe -ArgumentList $args -PassThru -WindowStyle Normal
  $deadline = (Get-Date).AddSeconds(25)
  while ((Get-Date) -lt $deadline) {
    if ($p.HasExited) { break }
    if (Test-Path $out) {
      Start-Sleep -Milliseconds 200
      if (-not $p.HasExited) {
        try { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue } catch {}
      }
      break
    }
    Start-Sleep -Milliseconds 250
  }
  if (-not (Test-Path $out)) { throw ("screenshot missing for " + $Mode + " -> " + $out) }
  $len = (Get-Item $out).Length
  if ($len -lt 20000) { throw ("screenshot too small for " + $Mode + ": " + $len) }
  Write-Host ("OK " + $out + " (" + $len + " bytes)")
}

if (-not (Test-Path $Exe)) { throw ("missing " + $Exe + " - build Release first") }

$ApprovedSrc = Join-Path $WinRoot "gui\src\Nyxveil.App\Assets\Backgrounds\reference.png"
if (Test-Path $ApprovedSrc) {
  Copy-Item $ApprovedSrc (Join-Path $QaDir "approved-reference.png") -Force
}

Shot "disconnected" "final-home-disconnected.png"
Shot "connected" "final-home-connected.png"
Shot "connecting" "final-home-connecting.png"
Shot "error" "final-home-error.png"
Write-Host ("QA shots in " + $QaDir)
