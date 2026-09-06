@echo off
:: Elevated portable installer when Inno Setup is unavailable.
net session >nul 2>&1 || (echo Require Administrator & exit /b 1)
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0Install.ps1"
