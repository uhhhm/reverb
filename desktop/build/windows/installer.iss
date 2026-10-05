; Reverb's Windows setup program, built by the release workflow with Inno
; Setup from the extracted install bundle (package-windows.ps1):
;
;   iscc /DAppVersion=1.2.3 /DSourceDir=<bundle>\Reverb /DOutputDir=dist
;        /DOutputBaseFilename=Reverb-1.2.3-windows-amd64-setup installer.iss
;
; It installs per user, without elevation, under %LOCALAPPDATA%\Programs, so
; the desktop updater can replace reverb-desktop.exe in place. The library and
; settings live in %AppData%\reverb and are never touched by uninstalling.

#ifndef AppVersion
  #error AppVersion is required
#endif
#ifndef SourceDir
  #error SourceDir is required
#endif
#ifndef OutputDir
  #define OutputDir "."
#endif
#ifndef OutputBaseFilename
  #define OutputBaseFilename "Reverb-setup"
#endif

[Setup]
AppId={{6F1B7C2E-3D4A-4E8B-9C1F-5A2D7E9B0C34}
AppName=Reverb
AppVersion={#AppVersion}
AppVerName=Reverb {#AppVersion}
AppPublisher=Reverb
DefaultDirName={localappdata}\Programs\Reverb
DefaultGroupName=Reverb
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir={#OutputDir}
OutputBaseFilename={#OutputBaseFilename}
SetupIconFile={#SourcePath}\icon.ico
UninstallDisplayIcon={app}\reverb-desktop.exe
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "{#SourceDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs; Excludes: "install-shortcut.ps1,uninstall-shortcut.ps1"

[Icons]
Name: "{autoprograms}\Reverb"; Filename: "{app}\reverb-desktop.exe"; WorkingDir: "{app}"
Name: "{autodesktop}\Reverb"; Filename: "{app}\reverb-desktop.exe"; WorkingDir: "{app}"; Tasks: desktopicon

[Run]
Filename: "{app}\reverb-desktop.exe"; Description: "{cm:LaunchProgram,Reverb}"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; What the app adds beside itself after install: the updater's backups of the
; replaced executable, the tools it updates and Python's bytecode caches.
Type: files; Name: "{app}\reverb-desktop.exe.*"
Type: filesandordirs; Name: "{app}\bin"
Type: filesandordirs; Name: "{app}\python"
Type: dirifempty; Name: "{app}"
