#Requires -Version 5.1
<#
.SYNOPSIS
  Build the HarfBuzz + FreeType static libraries Meltemi needs on Windows,
  using the existing WinLibs MinGW-w64 toolchain.

.DESCRIPTION
  Default output prefix: third_party/windows-text, laid out as:
    include/harfbuzz/...   include/freetype2/...
    lib/libharfbuzz.a     lib/libfreetype.a
  Only MinGW's bundled cmake / ninja / gcc / ar / ranlib are required. No
  MSYS2, vcpkg, Python or system tar is needed (cmake -E tar extracts .xz).

.PARAMETER MingwRoot
  MinGW-w64 root directory (containing bin\gcc.exe). When omitted it is
  inferred from gcc on PATH or from a WinGet WinLibs package.

.PARAMETER OutDir
  Install prefix. Defaults to third_party\windows-text under the repo.

.PARAMETER WorkDir
  Download/build scratch directory. Defaults to %TEMP%\meltemi-windows-text-deps.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File scripts\build-windows-text-deps.ps1
#>
[CmdletBinding()]
param(
    [string]$MingwRoot,
    [string]$OutDir,
    [string]$WorkDir,
    [string]$HarfBuzzVersion = "12.3.2",
    [string]$FreeTypeVersion = "2.13.3"
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

if (-not $OutDir) {
    $scriptDir = if ($PSScriptRoot) { $PSScriptRoot } else { Split-Path -Parent $MyInvocation.MyCommand.Path }
    $OutDir = Join-Path $scriptDir "..\third_party\windows-text"
}
if (-not $WorkDir) {
    $WorkDir = Join-Path $env:TEMP "meltemi-windows-text-deps"
}

function Resolve-MingwRoot {
    param([string]$Hint)
    if ($Hint) { return (Resolve-Path -LiteralPath $Hint).Path }

    $gcc = Get-Command gcc.exe -ErrorAction SilentlyContinue
    if ($gcc) { return (Split-Path -Parent (Split-Path -Parent $gcc.Source)) }

    $packages = Join-Path $env:LOCALAPPDATA "Microsoft\WinGet\Packages"
    if (Test-Path -LiteralPath $packages) {
        foreach ($dir in Get-ChildItem -LiteralPath $packages -Directory -ErrorAction SilentlyContinue) {
            if ($dir.Name -notlike "BrechtSanders.WinLibs*") { continue }
            $candidate = Join-Path $dir.FullName "mingw64"
            if (Test-Path -LiteralPath (Join-Path $candidate "bin\gcc.exe")) { return $candidate }
        }
    }
    throw "Cannot find MinGW-w64 gcc. Add mingw64\bin to PATH or pass -MingwRoot."
}

$mingw = Resolve-MingwRoot $MingwRoot
$cmake = Join-Path $mingw "bin\cmake.exe"
$ninja = Join-Path $mingw "bin\ninja.exe"
$gcc = Join-Path $mingw "bin\gcc.exe"
$gxx = Join-Path $mingw "bin\g++.exe"
$ar = Join-Path $mingw "bin\ar.exe"
$ranlib = Join-Path $mingw "bin\ranlib.exe"

foreach ($tool in @($cmake, $ninja, $gcc, $gxx, $ar, $ranlib)) {
    if (-not (Test-Path -LiteralPath $tool)) { throw "Missing build tool: $tool" }
}

New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
$OutDir = (Resolve-Path -LiteralPath $OutDir).Path

function Get-Source {
    param([string]$Url, [string]$Archive, [string]$SourceDir)
    if (Test-Path -LiteralPath $SourceDir) { return }
    if (-not (Test-Path -LiteralPath $Archive)) {
        Write-Host "Downloading $Url"
        Invoke-WebRequest -Uri $Url -OutFile $Archive -TimeoutSec 300
    }
    Write-Host "Extracting $Archive"
    Push-Location $WorkDir
    try { & $cmake -E tar xf $Archive } finally { Pop-Location }
    if (-not (Test-Path -LiteralPath $SourceDir)) { throw "Extraction did not produce $SourceDir" }
}

$ftArchive = Join-Path $WorkDir "freetype-$FreeTypeVersion.tar.xz"
$ftSource = Join-Path $WorkDir "freetype-$FreeTypeVersion"
$hbArchive = Join-Path $WorkDir "harfbuzz-$HarfBuzzVersion.tar.xz"
$hbSource = Join-Path $WorkDir "harfbuzz-$HarfBuzzVersion"

Get-Source -Url "https://download.savannah.gnu.org/releases/freetype/freetype-$FreeTypeVersion.tar.xz" -Archive $ftArchive -SourceDir $ftSource
Get-Source -Url "https://github.com/harfbuzz/harfbuzz/releases/download/$HarfBuzzVersion/harfbuzz-$HarfBuzzVersion.tar.xz" -Archive $hbArchive -SourceDir $hbSource

Write-Host "Building FreeType $FreeTypeVersion"
& $cmake -G Ninja -S $ftSource -B (Join-Path $WorkDir "build-ft") `
    -DCMAKE_BUILD_TYPE=Release `
    -DCMAKE_C_COMPILER="$gcc" -DCMAKE_AR="$ar" -DCMAKE_RANLIB="$ranlib" `
    -DCMAKE_MAKE_PROGRAM="$ninja" `
    -DBUILD_SHARED_LIBS=OFF `
    -DFT_DISABLE_ZLIB=TRUE -DFT_DISABLE_BZIP2=TRUE -DFT_DISABLE_PNG=TRUE `
    -DFT_DISABLE_HARFBUZZ=TRUE -DFT_DISABLE_BROTLI=TRUE `
    -DCMAKE_INSTALL_PREFIX="$OutDir"
if ($LASTEXITCODE -ne 0) { throw "FreeType configure failed" }
& $cmake --build (Join-Path $WorkDir "build-ft")
if ($LASTEXITCODE -ne 0) { throw "FreeType build failed" }
& $cmake --install (Join-Path $WorkDir "build-ft")
if ($LASTEXITCODE -ne 0) { throw "FreeType install failed" }

Write-Host "Building HarfBuzz $HarfBuzzVersion"
& $cmake -G Ninja -S $hbSource -B (Join-Path $WorkDir "build-hb") `
    -DCMAKE_BUILD_TYPE=Release `
    -DCMAKE_C_COMPILER="$gcc" -DCMAKE_CXX_COMPILER="$gxx" `
    -DCMAKE_AR="$ar" -DCMAKE_RANLIB="$ranlib" -DCMAKE_MAKE_PROGRAM="$ninja" `
    -DBUILD_SHARED_LIBS=OFF `
    -DHB_HAVE_FREETYPE=ON -DHB_HAVE_GLIB=OFF -DHB_HAVE_ICU=OFF `
    -DHB_HAVE_GRAPHITE2=OFF -DHB_HAVE_CAIRO=OFF `
    -DHB_BUILD_UTILS=OFF -DHB_BUILD_SUBSET=OFF `
    -DCMAKE_PREFIX_PATH="$OutDir" -DCMAKE_INSTALL_PREFIX="$OutDir"
if ($LASTEXITCODE -ne 0) { throw "HarfBuzz configure failed" }
& $cmake --build (Join-Path $WorkDir "build-hb")
if ($LASTEXITCODE -ne 0) { throw "HarfBuzz build failed" }
& $cmake --install (Join-Path $WorkDir "build-hb")
if ($LASTEXITCODE -ne 0) { throw "HarfBuzz install failed" }

$includeHB = (Join-Path $OutDir "include\harfbuzz") -replace '\\', '/'
$includeFT = (Join-Path $OutDir "include\freetype2") -replace '\\', '/'
$libDir = (Join-Path $OutDir "lib") -replace '\\', '/'

Write-Host ""
Write-Host "Done. Build Meltemi with:"
Write-Host "  `$env:CGO_ENABLED = '1'"
Write-Host "  `$env:CGO_CFLAGS  = '-I$includeHB -I$includeFT'"
Write-Host "  `$env:CGO_LDFLAGS = '-L$libDir -lharfbuzz -lfreetype -lstdc++ -lm'"
Write-Host "  go build -tags meltemi_text_cgo ./..."
