#Requires -Version 5.1
$ErrorActionPreference = "Stop"
Add-Type -AssemblyName System.Drawing

$QaDir = "D:\Nyxveil\clients\windows\dist\reference-qa"
Get-ChildItem (Join-Path $QaDir "*.png") | ForEach-Object {
  $img = [System.Drawing.Image]::FromFile($_.FullName)
  "{0}`t{1}x{2}`t{3} bytes" -f $_.Name, $img.Width, $img.Height, $_.Length
  $img.Dispose()
}

# Sample center pixel of connected shot to verify content type
$probe = Join-Path $QaDir "final-home-connected.png"
$bmp = [System.Drawing.Bitmap]::FromFile($probe)
$c1 = $bmp.GetPixel([int]($bmp.Width / 2), [int]($bmp.Height * 0.15))
$c2 = $bmp.GetPixel([int]($bmp.Width / 2), [int]($bmp.Height * 0.35))
$c3 = $bmp.GetPixel(20, 20)
"center-top RGB={0},{1},{2}" -f $c1.R, $c1.G, $c1.B
"center-mid RGB={0},{1},{2}" -f $c2.R, $c2.G, $c2.B
"corner RGB={0},{1},{2}" -f $c3.R, $c3.G, $c3.B
$bmp.Dispose()
