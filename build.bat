@echo off
setlocal enabledelayedexpansion

echo ==========================================================
echo  [BUILD] cworker Unified Binary Builder (Output: .\bin\)
echo ==========================================================

REM 1. Check Go environment
where go >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Go compiler not found in PATH! Please install Go 1.21+.
    exit /b 1
)

for /f "tokens=*" %%v in ('go version') do set GO_VER=%%v
echo [INFO] Detected: %GO_VER%

REM 2. Ensure root bin directory exists
if not exist "%~dp0bin" mkdir "%~dp0bin"

REM 3. Optional test parameter (compiles test binaries directly to .\bin\)
if /i "%1"=="test" (
    echo.
    echo [STEP] Running unit tests and compiling test binaries to .\bin\...
    go test -v -o "%~dp0bin" ./...
    if !ERRORLEVEL! neq 0 (
        echo [ERROR] Unit tests failed.
        exit /b 1
    )
    echo [OK] All unit tests passed. Test binaries saved in .\bin\
    echo.
)

REM 4. Compile standalone production binary directly to .\bin\cw.exe
echo [STEP] Building production binary to bin\cw.exe with -ldflags="-s -w"...
go build -ldflags="-s -w" -o "%~dp0bin\cw.exe" .
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Build failed.
    exit /b %ERRORLEVEL%
)

REM 5. Check binary info
if not exist "%~dp0bin\cw.exe" (
    echo [ERROR] bin\cw.exe was not generated.
    exit /b 1
)

for %%F in ("%~dp0bin\cw.exe") do set SIZE=%%~zF
set /a SIZE_MB=!SIZE! / 1048576
set /a SIZE_KB=!SIZE! / 1024

echo.
echo ==========================================================
echo  [SUCCESS] Production binary generated in .\bin\cw.exe
echo  - Path:     bin\cw.exe
echo  - Size:     !SIZE_KB! KB (~!SIZE_MB! MB)
echo  - Platform: Windows (JobObject + Service Native)
echo ==========================================================
echo.
echo Next steps:
echo   .\bin\cw.exe service install   - Deploy to ~/.cworker/bin and register service
echo   .\bin\cw.exe service start     - Start background service
echo.

REM 6. Optional full E2E test
if /i "%1"=="all" (
    echo [STEP] Compiling and running full cluster integration test suite...
    go test -c -o bin\cluster.test.exe .\test
    if errorlevel 1 exit /b 1
    .\bin\cluster.test.exe
    if errorlevel 1 exit /b 1
)

exit /b 0
