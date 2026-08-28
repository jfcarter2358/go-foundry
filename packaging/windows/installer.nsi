; installer.nsi — the Windows installer.
;
; Built with NSIS (`makensis`), which packages on Linux — `apt install nsis` —
; so the installer is produced by the same WSL toolchain that already
; cross-compiles the binary with mingw. Inno Setup makes a better-looking
; installer and needs Wine to build from here; that trade is the whole reason
; this is NSIS.
;
; Everything specific to a game arrives as a /D define from the Makefile, which
; reads them from the .foundrypack stamp foundrypack wrote. Nothing about a
; particular project is hard-coded in this file.
;
;   makensis -DAPPNAME="My Game" -DVERSION=1.0.0 -DEXENAME=my-game.exe \
;            -DDATADIR="My Game" -DSRCDIR=build/install-windows \
;            -DOUTFILE=build/MyGame-Setup.exe packaging/windows/installer.nsi
;
; A per-user install, deliberately. $LOCALAPPDATA\Programs needs no
; administrator and raises no UAC prompt, which for a game is the difference
; between "it installed" and "Windows asked me something alarming". The engine
; writes nothing into its install directory in a release build — settings, saves
; and the log all go to %APPDATA%\<data dir> — so nothing is lost by the
; install tree being somewhere the user can write but the game never does.

!ifndef APPNAME
  !error "APPNAME is required (-DAPPNAME=...)"
!endif
!ifndef EXENAME
  !error "EXENAME is required (-DEXENAME=...)"
!endif
!ifndef SRCDIR
  !error "SRCDIR is required (-DSRCDIR=...)"
!endif
!ifndef OUTFILE
  !error "OUTFILE is required (-DOUTFILE=...)"
!endif
!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef DATADIR
  !define DATADIR "${APPNAME}"
!endif

; The uninstall entry's registry key. Keyed on the name rather than on a GUID
; so that re-running the installer for the same game upgrades in place instead
; of stacking a second entry in Add/Remove Programs.
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}"

Name "${APPNAME}"
OutFile "${OUTFILE}"
Unicode true
RequestExecutionLevel user
InstallDir "$LOCALAPPDATA\Programs\${APPNAME}"
InstallDirRegKey HKCU "Software\${APPNAME}" "InstallDir"
ShowInstDetails show
ShowUninstDetails show
SetCompressor /SOLID lzma

VIProductVersion "0.0.0.0"
VIAddVersionKey "ProductName" "${APPNAME}"
VIAddVersionKey "FileDescription" "${APPNAME} installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"

!include "MUI2.nsh"
; GetSize, for the Add/Remove Programs size estimate.
!include "FileFunc.nsh"

!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${EXENAME}"
!define MUI_FINISHPAGE_RUN_TEXT "Play ${APPNAME}"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Section "Game" SecGame
  SectionIn RO

  ; A reinstall over an older version has to remove the old content, not merge
  ; with it: a level or texture that was renamed between releases would
  ; otherwise stay behind for ever, and the game would keep finding it.
  RMDir /r "$INSTDIR\shaders"
  RMDir /r "$INSTDIR\levels"
  RMDir /r "$INSTDIR\textures"
  RMDir /r "$INSTDIR\entities"
  RMDir /r "$INSTDIR\scripts"
  RMDir /r "$INSTDIR\models"
  RMDir /r "$INSTDIR\ui"
  RMDir /r "$INSTDIR\fonts"
  RMDir /r "$INSTDIR\sounds"
  RMDir /r "$INSTDIR\particles"
  RMDir /r "$INSTDIR\detail_presets"

  SetOutPath "$INSTDIR"
  File /r "${SRCDIR}\*.*"

  WriteRegStr HKCU "Software\${APPNAME}" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\${APPNAME}" "Version" "${VERSION}"

  ; Add/Remove Programs. EstimatedSize is in KiB and is what the control panel
  ; shows; $0 comes back from GetSize in KiB already.
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  WriteRegStr   HKCU "${UNINSTKEY}" "DisplayName"     "${APPNAME}"
  WriteRegStr   HKCU "${UNINSTKEY}" "DisplayVersion"  "${VERSION}"
  WriteRegStr   HKCU "${UNINSTKEY}" "DisplayIcon"     "$INSTDIR\${EXENAME}"
  WriteRegStr   HKCU "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr   HKCU "${UNINSTKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoRepair" 1
  WriteRegDWORD HKCU "${UNINSTKEY}" "EstimatedSize" "$0"

  WriteUninstaller "$INSTDIR\uninstall.exe"
SectionEnd

Section "Start Menu shortcut" SecStartMenu
  CreateDirectory "$SMPROGRAMS\${APPNAME}"
  CreateShortcut "$SMPROGRAMS\${APPNAME}\${APPNAME}.lnk" "$INSTDIR\${EXENAME}" "" "$INSTDIR\${EXENAME}" 0
  CreateShortcut "$SMPROGRAMS\${APPNAME}\Uninstall ${APPNAME}.lnk" "$INSTDIR\uninstall.exe"
SectionEnd

Section /o "Desktop shortcut" SecDesktop
  CreateShortcut "$DESKTOP\${APPNAME}.lnk" "$INSTDIR\${EXENAME}" "" "$INSTDIR\${EXENAME}" 0
SectionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecGame}      "${APPNAME} and everything it needs to run."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecStartMenu} "A Start Menu entry for ${APPNAME}."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDesktop}   "A shortcut on the desktop."
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "Uninstall"
  ; The install tree, all of which this installer put there.
  RMDir /r "$INSTDIR"

  Delete "$DESKTOP\${APPNAME}.lnk"
  RMDir /r "$SMPROGRAMS\${APPNAME}"

  DeleteRegKey HKCU "${UNINSTKEY}"
  DeleteRegKey HKCU "Software\${APPNAME}"

  ; Saves and settings, in %APPDATA%\${DATADIR}, are deliberately left alone
  ; unless the player asks — an uninstall is very often a reinstall, and losing
  ; a save to one is not recoverable.
  IfFileExists "$APPDATA\${DATADIR}\*.*" 0 done
    MessageBox MB_YESNO|MB_ICONQUESTION \
      "Also delete your ${APPNAME} saves and settings?$\n$\n$APPDATA\${DATADIR}" \
      IDNO done
    RMDir /r "$APPDATA\${DATADIR}"
  done:
SectionEnd
