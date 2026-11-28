@echo off
setlocal
call "C:\Program Files (x86)\Microsoft Visual Studio\18\BuildTools\VC\Auxiliary\Build\vcvars64.bat" >nul
if errorlevel 1 exit /b 1

if not exist build mkdir build

echo Building server_hello_plugin.dll...
cl /nologo /O2 /MT /W3 /utf-8 /D_CRT_SECURE_NO_WARNINGS /LD ^
    server_hello_plugin.c /Fe:build\server_hello_plugin.dll /Fo:build\server_hello_plugin.obj
if errorlevel 1 exit /b 1

echo Building client_hello_plugin.dll...
cl /nologo /O2 /MT /W3 /utf-8 /D_CRT_SECURE_NO_WARNINGS /LD ^
    client_hello_plugin.c /Fe:build\client_hello_plugin.dll /Fo:build\client_hello_plugin.obj
if errorlevel 1 exit /b 1

echo Done: build\server_hello_plugin.dll and build\client_hello_plugin.dll
exit /b 0
