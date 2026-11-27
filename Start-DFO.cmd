@echo off
rem Alias kept because five documents still name this file as the entry point.
rem The real entry is 启动服务端.cmd (which runs the preflight first).
call "%~dp0启动服务端.cmd" %*
exit /b %ERRORLEVEL%
