$ErrorActionPreference = "Stop"

$programs = if ($env:REVERB_START_MENU_DIR) {
    [IO.Path]::GetFullPath($env:REVERB_START_MENU_DIR)
} else {
    [Environment]::GetFolderPath("Programs")
}
$shortcutPath = Join-Path $programs "Reverb.lnk"
if (Test-Path -LiteralPath $shortcutPath) {
    Remove-Item -LiteralPath $shortcutPath -Force
    Write-Host "Removed $shortcutPath"
} else {
    Write-Host "The Reverb Start Menu shortcut is not installed."
}

Write-Host "The extracted Reverb folder and app data were not removed."
