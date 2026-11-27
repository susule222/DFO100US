@echo off
rem Start the game client against the server that is already running.
rem The launcher starts both; use this when one had to be split because a game
rem window was still open (the game allows a single instance and protects its
rem own process, so an old window has to be closed by hand).
chcp 65001 >nul
cd /d "%~dp0"
set "PYTHONIOENCODING=utf-8"

where python >nul 2>&1
if errorlevel 1 (
    echo Python not found on PATH. Install Python 3.10+ or run the .py files with a full path.
    pause
    exit /b 1
)

python "%~dp0tools\us100_client.py"
set "CODE=%ERRORLEVEL%"
if not "%CODE%"=="0" pause
exit /b %CODE%
