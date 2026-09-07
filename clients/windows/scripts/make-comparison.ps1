#Requires -Version 5.1
$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Drawing

$Qa = "D:\Nyxveil\clients\windows\dist\reference-qa"
$refPath = Join-Path $Qa "approved-reference.png"
$curPath = Join-Path $Qa "final-home-connected.png"
$outPath = Join-Path $Qa "comparison-connected.png"

$ref = [System.Drawing.Image]::FromFile($refPath)
$cur = [System.Drawing.Image]::FromFile($curPath)
$targetH = 900
$refW = [int]($ref.Width * ($targetH / [double]$ref.Height))
$curW = [int]($cur.Width * ($targetH / [double]$cur.Height))
$gap = 24
$pad = 40
$bmp = New-Object System.Drawing.Bitmap (($refW + $curW + $gap + $pad * 2), ($targetH + $pad * 2 + 36))
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.Clear([System.Drawing.Color]::FromArgb(12, 16, 22))
$g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
$font = New-Object System.Drawing.Font("Segoe UI", 12, [System.Drawing.FontStyle]::Bold)
$brush = [System.Drawing.Brushes]::WhiteSmoke
$g.DrawString("APPROVED REFERENCE", $font, $brush, $pad, 12)
$g.DrawString("CURRENT 1.1.1 CONNECTED", $font, $brush, ($pad + $refW + $gap), 12)
$g.DrawImage($ref, $pad, ($pad + 28), $refW, $targetH)
$g.DrawImage($cur, ($pad + $refW + $gap), ($pad + 28), $curW, $targetH)
$bmp.Save($outPath, [System.Drawing.Imaging.ImageFormat]::Png)
$g.Dispose(); $bmp.Dispose(); $ref.Dispose(); $cur.Dispose()
Write-Host "wrote $outPath"
