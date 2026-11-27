@echo off
setlocal
call "C:\Program Files (x86)\Microsoft Visual Studio\18\BuildTools\VC\Auxiliary\Build\vcvars64.bat" >nul
if errorlevel 1 exit /b 1
rem user32.lib is needed by the dialog watcher (EnumWindows/GetWindowTextW/
rem PostMessageW/GetWindowThreadProcessId). The hook functions themselves still
rem resolve their targets with GetProcAddress, so this is the only static
rem user32 dependency.
cl /nologo /O2 /MT /W3 /utf-8 /D_CRT_SECURE_NO_WARNINGS /LD ngs_stub.c /link /DEF:ngs_stub.def /OUT:NGClient64.aes user32.lib
exit /b %errorlevel%
