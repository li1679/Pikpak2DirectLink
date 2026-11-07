param([switch]$NoBundle, [string]$GoExecutable = 'go')
$ErrorActionPreference = 'Stop'
$project = Split-Path $PSScriptRoot -Parent
$target = 'x86_64-pc-windows-msvc'
$version = (Get-Content (Join-Path $PSScriptRoot 'src-tauri\tauri.conf.json') -Raw | ConvertFrom-Json).version
if (-not (Get-Command $GoExecutable -ErrorAction SilentlyContinue)) { throw 'Go compiler not found. Pass -GoExecutable with its full path.' }
Push-Location $project
try {
  & (Join-Path $PSScriptRoot 'generate-icon.ps1')
  Push-Location web
  try { & npm.cmd run build; if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed' } } finally { Pop-Location }
  $binaries = Join-Path $PSScriptRoot 'src-tauri\binaries'
  New-Item -ItemType Directory -Force $binaries | Out-Null
  & $GoExecutable build -trimpath -ldflags "-s -w -X pikpak2directlink/internal/version.Version=desktop-v$version" -o (Join-Path $binaries "pikpak-service-$target.exe") ./cmd/desktop-service
  if ($LASTEXITCODE -ne 0) { throw 'Desktop service build failed' }
  Push-Location $PSScriptRoot
  try {
    $arguments = @('exec', 'tauri', 'build', '--', '--target', $target)
    if ($NoBundle) { $arguments += '--no-bundle' }
    & npm.cmd @arguments
    if ($LASTEXITCODE -ne 0) { throw 'Desktop shell build failed' }
  } finally { Pop-Location }
  $targetRoot = if ($env:CARGO_TARGET_DIR) { $env:CARGO_TARGET_DIR } else { Join-Path $PSScriptRoot 'src-tauri\target' }
  $release = Join-Path $targetRoot "$target\release"
  $portable = Join-Path $project 'dist\PikPak-Personal'
  New-Item -ItemType Directory -Force $portable | Out-Null
  Copy-Item (Join-Path $release 'pikpak-personal.exe') (Join-Path $portable 'PikPak Personal.exe') -Force
  Copy-Item (Join-Path $binaries "pikpak-service-$target.exe") (Join-Path $portable 'pikpak-service.exe') -Force
  if (-not $NoBundle) {
    Get-ChildItem (Join-Path $release 'bundle\nsis') -Filter '*.exe' | Copy-Item -Destination (Join-Path $project 'dist') -Force
  }
  Write-Host "Desktop output: $(Join-Path $project 'dist')"
} finally { Pop-Location }
