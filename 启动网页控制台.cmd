@echo off
setlocal EnableExtensions
chcp 65001 >nul

for %%I in ("%~dp0.") do set "ROOT=%%~fI"
pwsh.exe -NoProfile -ExecutionPolicy Bypass -File "%ROOT%\scripts\start-web-console.ps1" -Root "%ROOT%"
if errorlevel 1 (
  echo 网页控制台启动失败。
  pause
  exit /b 1
)
endlocal
