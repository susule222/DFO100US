@echo off
rem One-click start: preflight -> PostgreSQL/Redis -> server -> client.
rem The preflight refuses to start anything if the client path, the server
rem binary, port 7001 or the database location is wrong, so a failed start
rem leaves no orphaned process behind.
chcp 65001 >nul
cd /d "%~dp0"
set "PYTHONIOENCODING=utf-8"

where python >nul 2>&1
if errorlevel 1 (
    echo Python not found on PATH. Install Python 3.10+ or run the .py files with a full path.
    pause
    exit /b 1
)

python "%~dp0tools\us100_interactive.py" 3 normal %*
set "CODE=%ERRORLEVEL%"
if not "%CODE%"=="0" (
    echo.
    echo Launcher exited with code %CODE%.
    pause
)
exit /b %CODE%
