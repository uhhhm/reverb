param(
    [Parameter(Mandatory = $true)][string]$Binary,
    [Parameter(Mandatory = $true)][string]$Output
)

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "../..")).Path
$binaryPath = (Resolve-Path $Binary).Path
$outputPath = [IO.Path]::GetFullPath($Output)
$stage = Join-Path ([IO.Path]::GetDirectoryName($outputPath)) "stage-windows"
$bundle = Join-Path $stage "Reverb"

$required = @(
    "ffmpeg.exe",
    "navidrome.exe",
    "deno.exe",
    "spotdl.exe",
    "yt-dlp.exe"
)
foreach ($tool in $required) {
    $path = Join-Path $PSScriptRoot "bin/$tool"
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Missing $path; run the Windows desktop dependency fetch first"
    }
}
$python = Join-Path $PSScriptRoot "python/python.exe"
if (-not (Test-Path -LiteralPath $python -PathType Leaf)) {
    throw "Missing $python; run the Windows desktop dependency fetch first"
}

Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path (Join-Path $bundle "bin") -Force | Out-Null
Copy-Item -LiteralPath $binaryPath -Destination (Join-Path $bundle "reverb-desktop.exe")
foreach ($tool in $required) {
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot "bin/$tool") -Destination (Join-Path $bundle "bin/$tool")
}
Copy-Item -LiteralPath (Join-Path $PSScriptRoot "python") -Destination (Join-Path $bundle "python") -Recurse
Copy-Item -LiteralPath (Join-Path $root "desktop/build/windows/install-shortcut.ps1") -Destination $bundle
Copy-Item -LiteralPath (Join-Path $root "desktop/build/windows/uninstall-shortcut.ps1") -Destination $bundle

Remove-Item -LiteralPath $outputPath -Force -ErrorAction SilentlyContinue
Compress-Archive -Path $bundle -DestinationPath $outputPath
Remove-Item -LiteralPath $stage -Recurse -Force
Write-Host "bundle: $outputPath"
