@echo off
rem 构建单文件 mes-sim.exe（需要 Go 1.21+）
cd /d %~dp0

set GOEXE=go.exe
where %GOEXE% >nul 2>nul
if %errorlevel%==0 (
  set GOCMD=go
) else if exist "%USERPROFILE%\.zcode-tools\go\bin\go.exe" (
  set GOCMD=%USERPROFILE%\.zcode-tools\go\bin\go.exe
) else (
  echo [错误] 未找到 Go，请安装 Go 1.21+ 或将其便携版解压到 %USERPROFILE%\.zcode-tools\go
  exit /b 1
)

%GOCMD% vet ./... || exit /b 1
%GOCMD% build -trimpath -ldflags "-s -w" -o mes-sim.exe .
echo 构建完成: mes-sim.exe
pause
