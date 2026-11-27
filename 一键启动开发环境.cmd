@echo off
rem 一键启动 US100 开发环境：服务端 + 客户端。
rem db100 之后没有任何外部存储进程：数据库是 server-us100\runtime\us100.db
rem 单文件，服务端启动时自己打开（毫秒级），所以不再有存储守护/预热阶段。
rem 构建也是自动的：tools\go（项目自有工具链）只在源码比二进制新时重编。
chcp 65001 >nul
cd /d "%~dp0"
set "PYTHONIOENCODING=utf-8"

where python >nul 2>&1
if errorlevel 1 (
    echo Python not found on PATH.
    pause
    exit /b 1
)

echo [start] environment (build + server + client)...
python "%~dp0tools\us100_interactive.py" 3 normal
set "CODE=%ERRORLEVEL%"
if not "%CODE%"=="0" pause
exit /b %CODE%
