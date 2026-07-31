@echo off
setlocal EnableExtensions
chcp 65001 >nul

for %%I in ("%~dp0.") do set "ROOT=%%~fI"
pwsh.exe -NoProfile -ExecutionPolicy Bypass -File "%ROOT%\scripts\start-watch.ps1" -Root "%ROOT%"
if errorlevel 1 (
  echo »½ÐÑ·þÎñÆô¶¯Ê§°Ü¡£
  pause
  exit /b 1
)
endlocal