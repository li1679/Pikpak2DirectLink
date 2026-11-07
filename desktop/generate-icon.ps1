# Generates the monochrome wordmark and every desktop icon from one drawing.
# No image-generation service or external font files are required.
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
$project = Split-Path $PSScriptRoot -Parent
$tauri = Join-Path $PSScriptRoot 'node_modules\.bin\tauri.cmd'
if (-not (Test-Path $tauri)) { throw 'Run npm.cmd ci in desktop before generating icons.' }
$public = Join-Path $project 'web\public'
$icons = Join-Path $PSScriptRoot 'src-tauri\icons'
$temporaryRoot = if ($env:PI_SCRATCH_DIR) { $env:PI_SCRATCH_DIR } else { [IO.Path]::GetTempPath() }
$temporary = Join-Path $temporaryRoot ('pikpak-icons-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force $icons, $temporary | Out-Null
$word = [Drawing.Drawing2D.GraphicsPath]::new()
$background = [Drawing.Drawing2D.GraphicsPath]::new()
$font = [Drawing.FontFamily]::new('Arial')
$format = [Drawing.StringFormat]::GenericTypographic
try {
  $word.AddString('pikpak', $font, [int][Drawing.FontStyle]::Bold, 176, [Drawing.PointF]::new(0, 0), $format)
  $bounds = $word.GetBounds()
  $scale = 440.0 / $bounds.Width
  $dx = 256 - ($bounds.X + $bounds.Width / 2) * $scale
  $dy = 256 - ($bounds.Y + $bounds.Height / 2) * $scale
  $matrix = [Drawing.Drawing2D.Matrix]::new([single]$scale, 0, 0, [single]$scale, [single]$dx, [single]$dy)
  try { $word.Transform($matrix) } finally { $matrix.Dispose() }

  # Rounded black square, no border, gradient, shadow, or decorative symbol.
  $diameter = 176
  $background.AddArc(0, 0, $diameter, $diameter, 180, 90)
  $background.AddArc(512 - $diameter, 0, $diameter, $diameter, 270, 90)
  $background.AddArc(512 - $diameter, 512 - $diameter, $diameter, $diameter, 0, 90)
  $background.AddArc(0, 512 - $diameter, $diameter, $diameter, 90, 90)
  $background.CloseFigure()

  $bitmap = [Drawing.Bitmap]::new(512, 512, [Drawing.Imaging.PixelFormat]::Format32bppArgb)
  $graphics = [Drawing.Graphics]::FromImage($bitmap)
  try {
    $graphics.Clear([Drawing.Color]::Transparent)
    $graphics.SmoothingMode = [Drawing.Drawing2D.SmoothingMode]::AntiAlias
    $graphics.PixelOffsetMode = [Drawing.Drawing2D.PixelOffsetMode]::HighQuality
    $graphics.FillPath([Drawing.Brushes]::Black, $background)
    $graphics.FillPath([Drawing.Brushes]::White, $word)
    $bitmap.Save((Join-Path $public 'app-icon.png'), [Drawing.Imaging.ImageFormat]::Png)
  } finally { $graphics.Dispose(); $bitmap.Dispose() }

  # Store outlined vector artwork so rendering never depends on an installed font.
  function Format-Point([Drawing.PointF]$point) {
    $culture = [Globalization.CultureInfo]::InvariantCulture
    return $point.X.ToString('0.###', $culture) + ' ' + $point.Y.ToString('0.###', $culture)
  }
  $points = $word.PathPoints
  $types = $word.PathTypes
  $path = [Text.StringBuilder]::new()
  for ($i = 0; $i -lt $points.Length; $i++) {
    $type = $types[$i] -band 7
    switch ($type) {
      0 { [void]$path.Append('M' + (Format-Point $points[$i]) + ' ') }
      1 { [void]$path.Append('L' + (Format-Point $points[$i]) + ' ') }
      3 {
        if ($i + 2 -ge $points.Length) { throw 'Invalid wordmark curve.' }
        [void]$path.Append('C' + (Format-Point $points[$i]) + ' ' + (Format-Point $points[$i + 1]) + ' ' + (Format-Point $points[$i + 2]) + ' ')
        $i += 2
      }
      default { throw 'Unexpected wordmark path type.' }
    }
    if ($types[$i] -band 128) { [void]$path.Append('Z ') }
  }
  $svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" role="img" aria-label="pikpak"><title>pikpak</title><rect width="512" height="512" rx="88" fill="#000"/><path fill="#fff" d="' + $path.ToString().Trim() + '"/></svg>'
  [IO.File]::WriteAllText((Join-Path $public 'app-icon.svg'), $svg, [Text.UTF8Encoding]::new($false))
  & $tauri icon (Join-Path $public 'app-icon.png') --output $temporary
  if ($LASTEXITCODE -ne 0) { throw 'Tauri icon generation failed.' }
  foreach ($file in @('icon.ico', '32x32.png', '128x128.png')) {
    Copy-Item (Join-Path $temporary $file) (Join-Path $icons $file) -Force
  }
  Write-Host 'Updated black-and-white pikpak PNG, outlined SVG, and desktop icons.'
} finally {
  $word.Dispose(); $background.Dispose(); $font.Dispose(); $format.Dispose()
  if (Test-Path $temporary) { Remove-Item $temporary -Recurse -Force }
}
