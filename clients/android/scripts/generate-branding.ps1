#Requires -Version 5.1
<#
.SYNOPSIS
  Generate Android launcher / notification icons from approved Nyxveil master artwork.
#>
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$RepoRoot = Split-Path -Parent (Split-Path -Parent $Root)
$Master = Join-Path $RepoRoot "Assets\Branding\nyxveil-master-1024.png"
if (-not (Test-Path $Master)) {
    throw "Approved master icon not found: $Master"
}
$HeroSrc = Join-Path $RepoRoot "clients\windows\gui\src\Nyxveil.App\Assets\Backgrounds\hero.png"
$Res = Join-Path $Root "app\src\main\res"
$BrandDir = Join-Path $Root "branding"
New-Item -ItemType Directory -Force -Path $BrandDir | Out-Null
Copy-Item -Force $Master (Join-Path $BrandDir "nyxveil-master-1024.png")

Add-Type -AssemblyName System.Drawing

# Hero source is often JPEG bytes with a .png name; re-encode to real PNG for AAPT2.
if (Test-Path $HeroSrc) {
    Copy-Item -Force $HeroSrc (Join-Path $BrandDir "hero-source.bin")
    $drawable = Join-Path $Res "drawable"
    New-Item -ItemType Directory -Force -Path $drawable | Out-Null
    $heroDst = Join-Path $drawable "nyxveil_hero.png"
    $heroImg = [System.Drawing.Image]::FromFile($HeroSrc)
    try {
        $w = [Math]::Min($heroImg.Width, 1920)
        $h = [int]([Math]::Round($heroImg.Height * ($w / [double]$heroImg.Width)))
        if ($h -lt 1) { $h = 1 }
        $bmp = New-Object System.Drawing.Bitmap $w, $h
        $g = [System.Drawing.Graphics]::FromImage($bmp)
        $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $g.DrawImage($heroImg, 0, 0, $w, $h)
        $g.Dispose()
        $bmp.Save($heroDst, [System.Drawing.Imaging.ImageFormat]::Png)
        $bmp.Dispose()
    } finally {
        $heroImg.Dispose()
    }
}

function New-ScaledPng([string]$srcPath, [string]$dstPath, [int]$size, [bool]$padForAdaptive = $false) {
    $src = [System.Drawing.Image]::FromFile($srcPath)
    try {
        $canvas = $size
        $bmp = New-Object System.Drawing.Bitmap $canvas, $canvas
        $g = [System.Drawing.Graphics]::FromImage($bmp)
        $g.Clear([System.Drawing.Color]::FromArgb(255, 11, 18, 32)) # Nyx navy
        $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::HighQuality
        $g.PixelOffsetMode = [System.Drawing.Drawing2D.PixelOffsetMode]::HighQuality
        if ($padForAdaptive) {
            # Safe zone ~66% for adaptive icon masks
            $inner = [int]([math]::Round($canvas * 0.66))
            $off = [int](($canvas - $inner) / 2)
            $g.DrawImage($src, $off, $off, $inner, $inner)
        } else {
            $g.DrawImage($src, 0, 0, $canvas, $canvas)
        }
        $dir = Split-Path $dstPath
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        $bmp.Save($dstPath, [System.Drawing.Imaging.ImageFormat]::Png)
        $g.Dispose()
        $bmp.Dispose()
    } finally {
        $src.Dispose()
    }
}

function New-MonoNotification([string]$srcPath, [string]$dstPath, [int]$size) {
    $src = [System.Drawing.Image]::FromFile($srcPath)
    try {
        $bmp = New-Object System.Drawing.Bitmap $size, $size
        $g = [System.Drawing.Graphics]::FromImage($bmp)
        $g.Clear([System.Drawing.Color]::Transparent)
        $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $tmp = New-Object System.Drawing.Bitmap $size, $size
        $gt = [System.Drawing.Graphics]::FromImage($tmp)
        $gt.Clear([System.Drawing.Color]::Transparent)
        $gt.DrawImage($src, 0, 0, $size, $size)
        $gt.Dispose()
        for ($y = 0; $y -lt $size; $y++) {
            for ($x = 0; $x -lt $size; $x++) {
                $c = $tmp.GetPixel($x, $y)
                if ($c.A -lt 16) {
                    $bmp.SetPixel($x, $y, [System.Drawing.Color]::Transparent)
                } else {
                    # white silhouette for status bar tinting
                    $bmp.SetPixel($x, $y, [System.Drawing.Color]::FromArgb($c.A, 255, 255, 255))
                }
            }
        }
        $tmp.Dispose()
        $dir = Split-Path $dstPath
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
        $bmp.Save($dstPath, [System.Drawing.Imaging.ImageFormat]::Png)
        $g.Dispose()
        $bmp.Dispose()
    } finally {
        $src.Dispose()
    }
}

$densities = @{
    "mipmap-mdpi"    = 48
    "mipmap-hdpi"    = 72
    "mipmap-xhdpi"   = 96
    "mipmap-xxhdpi"  = 144
    "mipmap-xxxhdpi" = 192
}
foreach ($kv in $densities.GetEnumerator()) {
    $dir = Join-Path $Res $kv.Key
    New-ScaledPng $Master (Join-Path $dir "ic_launcher.png") $kv.Value $false
    New-ScaledPng $Master (Join-Path $dir "ic_launcher_round.png") $kv.Value $false
    New-ScaledPng $Master (Join-Path $dir "ic_launcher_foreground.png")  ($kv.Value * 2) $true
}

# Adaptive foreground @xxxhdpi 432 (108dp * 4)
New-ScaledPng $Master (Join-Path $Res "drawable-xxxhdpi\ic_launcher_foreground.png") 432 $true
New-ScaledPng $Master (Join-Path $Res "drawable-xxhdpi\ic_launcher_foreground.png") 324 $true
New-ScaledPng $Master (Join-Path $Res "drawable-xhdpi\ic_launcher_foreground.png") 216 $true
New-ScaledPng $Master (Join-Path $Res "drawable-hdpi\ic_launcher_foreground.png") 162 $true
New-ScaledPng $Master (Join-Path $Res "drawable-mdpi\ic_launcher_foreground.png") 108 $true

New-MonoNotification $Master (Join-Path $Res "drawable-mdpi\ic_notification.png") 24
New-MonoNotification $Master (Join-Path $Res "drawable-hdpi\ic_notification.png") 36
New-MonoNotification $Master (Join-Path $Res "drawable-xhdpi\ic_notification.png") 48
New-MonoNotification $Master (Join-Path $Res "drawable-xxhdpi\ic_notification.png") 72
New-MonoNotification $Master (Join-Path $Res "drawable-xxxhdpi\ic_notification.png") 96

Write-Host "Branding icons generated from approved master."
