#Requires -Version 5.1
param(
  [string]$Mode = "connected",
  [string]$OutName = "_probe-connected.png"
)
$ErrorActionPreference = "Stop"

Add-Type -AssemblyName System.Drawing
Add-Type @"
using System;
using System.Runtime.InteropServices;
public struct RECT2 { public int Left; public int Top; public int Right; public int Bottom; }
public static class Win32B {
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT2 lpRect);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hWnd);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);
}
"@

$Exe = "D:\Nyxveil\clients\windows\gui\src\Nyxveil.App\bin\Release\net10.0-windows\win-x64\Nyxveil.exe"
$QaDir = "D:\Nyxveil\clients\windows\dist\reference-qa"
New-Item -ItemType Directory -Force -Path $QaDir | Out-Null

Get-Process -Name "Nyxveil" -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 500

$p = Start-Process -FilePath $Exe -ArgumentList ("--visual-qa=" + $Mode) -PassThru
Start-Sleep -Seconds 4

$hwnd = $p.MainWindowHandle
if ($hwnd -eq [IntPtr]::Zero) {
  Start-Sleep -Seconds 2
  $p.Refresh()
  $hwnd = $p.MainWindowHandle
}
if ($hwnd -eq [IntPtr]::Zero) { throw "no MainWindowHandle" }

[void][Win32B]::ShowWindow($hwnd, 9)
[void][Win32B]::SetForegroundWindow($hwnd)
Start-Sleep -Milliseconds 700

$rect = New-Object RECT2
[void][Win32B]::GetWindowRect($hwnd, [ref]$rect)
$w = $rect.Right - $rect.Left
$h = $rect.Bottom - $rect.Top
Write-Host ("hwnd={0} rect={1},{2} size={3}x{4} title={5}" -f $hwnd, $rect.Left, $rect.Top, $w, $h, $p.MainWindowTitle)

if ($w -lt 100 -or $h -lt 100) { throw "bad window size" }

$bmp = New-Object System.Drawing.Bitmap $w, $h
$g = [System.Drawing.Graphics]::FromImage($bmp)
$g.CopyFromScreen($rect.Left, $rect.Top, 0, 0, $bmp.Size)
$out = Join-Path $QaDir $OutName
$bmp.Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
$g.Dispose(); $bmp.Dispose()

Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
Write-Host ("saved {0}" -f $out)
