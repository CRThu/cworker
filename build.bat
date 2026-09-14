@echo off
setlocal enabledelayedexpansion

echo ==========================================================
echo  [BUILD] cworker Unified Binary Builder (Output: .\bin\)
echo ==========================================================

REM Parse command line arguments
set TARGET_MODE=default

if /i "%1"=="test" set TARGET_MODE=test
if /i "%1"=="all" set TARGET_MODE=all

REM 1. Check Go environment
where go >nul 2>&1
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Go compiler not found in PATH! Please install Go 1.21+.
    exit /b 1
)

for /f "tokens=*" %%v in ('go version') do set GO_VER=%%v
echo [INFO] Detected Go: %GO_VER%

REM 2. Compile frontend assets with Bun [output: pkg\ui\dist\]
where bun >nul 2>&1
if %ERRORLEVEL% neq 0 goto :no_bun

for /f "tokens=*" %%b in ('bun --version') do set BUN_VER=%%b
echo [INFO] Detected Bun: v%BUN_VER%
echo.
echo [STEP] Building frontend assets with Bun [web to pkg\ui\dist]...
pushd "%~dp0web"
call bun run build
if !ERRORLEVEL! neq 0 (
    popd
    echo [ERROR] Frontend build failed.
    exit /b 1
)
if "!TARGET_MODE!"=="test" (
    echo.
    echo [STEP] Running frontend unit tests [vitest]...
    call bun run test
    if !ERRORLEVEL! neq 0 (
        popd
        echo [ERROR] Frontend unit tests failed.
        exit /b 1
    )
    echo [OK] All frontend unit tests passed.
)
popd
echo [OK] Frontend assets successfully built into pkg\ui\dist\
goto :skip_web_build

:no_bun
if exist "%~dp0pkg\ui\dist\index.html" (
    echo [WARN] Bun not found in PATH! Using existing cached assets in pkg\ui\dist.
    goto :skip_web_build
)
echo [ERROR] Bun compiler not found in PATH and pkg\ui\dist\index.html does not exist!
echo         Please install Bun [https://bun.sh] to compile web frontend.
exit /b 1

:skip_web_build

REM 3. Ensure root bin directory exists
if not exist "%~dp0bin" mkdir "%~dp0bin"

REM 4. Optional backend test parameter
if "!TARGET_MODE!"=="test" (
    echo.
    echo [STEP] Running backend unit tests...
    go test -v ./...
    if !ERRORLEVEL! neq 0 (
        echo [ERROR] Backend unit tests failed.
        exit /b 1
    )
    echo [OK] All backend unit tests passed.
    echo.
)

REM 5. Compile standalone production binary directly to .\bin\cw.exe
echo [STEP] Building production binary to bin\cw.exe with -ldflags="-s -w"...
go build -ldflags="-s -w" -o "%~dp0bin\cw.exe" .
if %ERRORLEVEL% neq 0 (
    echo [ERROR] Build failed.
    exit /b %ERRORLEVEL%
)

REM 6. Check binary info
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

REM 7. Optional full E2E test
if "!TARGET_MODE!"=="all" (
    echo [STEP] Compiling and running full cluster integration test suite...
    go test -c -o bin\cluster.test.exe .\test
    if errorlevel 1 exit /b 1
    .\bin\cluster.test.exe
    if errorlevel 1 exit /b 1
)

exit /b 0
