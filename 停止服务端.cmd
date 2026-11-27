@echo off
rem Stop the session: server, client, then PostgreSQL and Redis.
rem Pass --keep-storage to leave the database running.
chcp 65001 >nul
cd /d "%~dp0"
set "PYTHONIOENCODING=utf-8"

python "%~dp0tools\us100_stop.py" %*
echo.
pause
