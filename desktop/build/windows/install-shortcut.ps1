$ErrorActionPreference = "Stop"

$bundle = Split-Path -Parent $MyInvocation.MyCommand.Path
$exe = Join-Path $bundle "reverb-desktop.exe"
if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) {
    throw "Reverb executable not found at $exe"
}

$programs = if ($env:REVERB_START_MENU_DIR) {
    New-Item -ItemType Directory -Path $env:REVERB_START_MENU_DIR -Force | Out-Null
    [IO.Path]::GetFullPath($env:REVERB_START_MENU_DIR)
} else {
    [Environment]::GetFolderPath("Programs")
}
$shortcutPath = Join-Path $programs "Reverb.lnk"
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($shortcutPath)
$shortcut.TargetPath = $exe
$shortcut.WorkingDirectory = $bundle
$shortcut.IconLocation = "$exe,0"
$shortcut.Description = "Reverb"
$shortcut.Save()

Write-Host "Installed the per-user Start Menu shortcut: $shortcutPath"
Write-Host "To remove it, run uninstall-shortcut.ps1. Your Reverb data is left intact."
