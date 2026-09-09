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
;
; Upgrading in place is the normal path, not a special case: running this
; installer over an existing install finds it, replaces it, and keeps the
; player's saves. That rests on three things, and all three have to hold or the
; player ends up uninstalling by hand.
;
;   1. It has to find the old install and stay there. $INSTDIR comes from the
;      registry and the Directory page is skipped on an upgrade, so a player
;      clicking Next cannot end up with a second copy in a second folder.
;
;   2. The old tree has to go, whole. Because the game never writes into its
;      install directory, everything under it belongs to this installer and can
;      simply be deleted — which is the only version of "remove the old content"
;      that does not rot, since a hand-kept list of directories to clear drifts
;      out of step with foundrypack's contentDirs the moment one is added. The
;      .foundrypack stamp is what proves the directory is ours before we
;      recursively delete it.
;
;   3. The game must not be running. An upgrade over a running game fails on the
;      locked .exe with an abort/retry/ignore box that looks like a broken
;      installer, so it is checked for up front and named.

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

; The stamp foundrypack leaves at the top of an install tree. Its presence is
; what licenses this installer to delete a directory recursively, so it is
; spelled the same here as in cmd/foundrypack (stampName).
!define STAMP ".foundrypack"

; Set in .onInit when an install of this game is already on the machine:
; $PrevVersion is what it says it is, "" if it never recorded one.
Var Upgrading
Var PrevVersion

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

; On an upgrade the Welcome page says so, and the Directory page does not run
; at all: the destination is settled — it is where the old install already is —
; and offering the choice is how a player ends up with two copies.
!define MUI_PAGE_CUSTOMFUNCTION_SHOW WelcomePageShow
!insertmacro MUI_PAGE_WELCOME
!define MUI_PAGE_CUSTOMFUNCTION_PRE DirectoryPagePre
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

; Find an existing install and adopt it.
;
; InstallDirRegKey has already pointed $INSTDIR at it; what this adds is the
; distinction between "the registry mentions a folder" and "that folder really
; is an install of ours", which is what the rest of the script needs before it
; will delete anything. A registry key whose folder has since been dragged to
; the bin is not an upgrade — it is a fresh install that happens to know where
; it used to live.
Function .onInit
  StrCpy $Upgrading 0
  StrCpy $PrevVersion ""

  ReadRegStr $0 HKCU "Software\${APPNAME}" "InstallDir"
  StrCmp $0 "" done
  IfFileExists "$0\${STAMP}" 0 done
    StrCpy $Upgrading 1
    StrCpy $INSTDIR $0
    ReadRegStr $PrevVersion HKCU "Software\${APPNAME}" "Version"
  done:
FunctionEnd

Function WelcomePageShow
  StrCmp $Upgrading 1 0 done
    StrCmp $PrevVersion "" 0 +3
      StrCpy $1 "Setup will update the copy of ${APPNAME} already installed in $INSTDIR to version ${VERSION}."
      Goto +2
      StrCpy $1 "Setup will update ${APPNAME} in $INSTDIR from version $PrevVersion to version ${VERSION}."
    SendMessage $mui.WelcomePage.Text ${WM_SETTEXT} 0 \
      "STR:$1$\r$\n$\r$\nYour saved games and settings are kept.$\r$\n$\r$\nClose ${APPNAME} before continuing, then click Next."
  done:
FunctionEnd

Function DirectoryPagePre
  StrCmp $Upgrading 1 0 +2
    Abort ; skip the page; the old install decides the destination
FunctionEnd

; Refuse to start while the game is running.
;
; Nothing else in the script would notice: File would simply fail on the locked
; .exe, part way through, with a stock abort/retry/ignore box naming a path the
; player has no reason to recognise — and a half-written install tree behind it.
; Opening the executable for append is the cheapest way to ask Windows whether
; anyone holds it, and needs no plugin.
Function CheckNotRunning
  IfFileExists "$INSTDIR\${EXENAME}" 0 done
  retry:
    ClearErrors
    FileOpen $0 "$INSTDIR\${EXENAME}" a
    IfErrors locked
    FileClose $0
    Goto done
  locked:
    IfSilent 0 ask
      SetErrorLevel 2
      Abort "${APPNAME} is running. Close it and run this installer again."
  ask:
    MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION \
      "${APPNAME} is still running.$\n$\nClose it, then click Retry." \
      IDRETRY retry
    Abort "${APPNAME} is running."
  done:
FunctionEnd

Section "Game" SecGame
  SectionIn RO

  Call CheckNotRunning

  ; An install over an older version replaces it rather than merging with it: a
  ; level or texture renamed between releases would otherwise stay behind for
  ; ever, and the game would keep finding it.
  ;
  ; The whole tree goes, not a list of directories, and that is the point — a
  ; list has to be kept in step with foundrypack's contentDirs by hand, and it
  ; already was not (chunks/ and video/ are packed and were never cleared). The
  ; game writes nothing under $INSTDIR in a release build, so there is nothing
  ; down there to preserve; saves and settings live in %APPDATA%\${DATADIR} and
  ; are not touched. The stamp is the guard: no stamp, no recursive delete, so a
  ; player who pointed the Directory page at an existing folder full of their
  ; own files loses nothing.
  IfFileExists "$INSTDIR\${STAMP}" 0 +3
    DetailPrint "Removing the previous installation..."
    RMDir /r "$INSTDIR"

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
