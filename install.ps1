<#
.SYNOPSIS
    cworker (cw) Windows 官方一键安装与自愈脚本
.DESCRIPTION
    自动从 GitHub Releases (crthu/cworker) 下载最新原生 cw.exe，
    校验 SHA-256 完整性，部署至用户目录 %USERPROFILE%\.cworker\bin，
    并自动将该目录安全加入当前用户环境变量 Path。
.PARAMETER Version
    指定安装版本号，如 "v1.0.0" 或 "latest"（默认 latest）
.PARAMETER Mirror
    指定加速反代或镜像站点前缀，如 "https://ghproxy.net"
.PARAMETER Proxy
    指定网络代理，如 "http://127.0.0.1:7890"
.PARAMETER NoPath
    若指定，则跳过环境变量 Path 的配置
.PARAMETER Force
    若指定，即使文件冲突也强制覆盖
.EXAMPLE
    irm https://raw.githubusercontent.com/crthu/cworker/main/install.ps1 | iex
.EXAMPLE
    & .\install.ps1 -Mirror "https://ghproxy.net"
#>

[CmdletBinding()]
param (
    [string]$Version = "latest",
    [string]$Mirror = "",
    [string]$Proxy = "",
    [switch]$NoPath,
    [switch]$Force
)

$ErrorActionPreference = "Stop"

# 统一输出高亮格式
function Write-Step {
    param([string]$Message)
    Write-Host "==> " -ForegroundColor Cyan -NoNewline
    Write-Host $Message
}

function Write-Success {
    param([string]$Message)
    Write-Host "[OK] " -ForegroundColor Green -NoNewline
    Write-Host $Message
}

function Write-Warn {
    param([string]$Message)
    Write-Host "[WARN] " -ForegroundColor Yellow -NoNewline
    Write-Host $Message
}

function Write-Failure {
    param([string]$Message)
    Write-Host "[ERROR] " -ForegroundColor Red -NoNewline
    Write-Host $Message
}

# 广播系统 WM_SETTINGCHANGE 消息使新 PATH 立即对所有新启动进程生效
function Broadcast-EnvChange {
    try {
        $typeDef = @"
using System;
using System.Runtime.InteropServices;
public class Win32Notifier {
    [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
    public static extern IntPtr SendMessageTimeout(
        IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam,
        uint fuFlags, uint uTimeout, out UIntPtr lpdwResult);
}
"@
        if (-not ([System.Management.Automation.PSTypeName]"Win32Notifier").Type) {
            Add-Type -TypeDefinition $typeDef
        }
        $result = [UIntPtr]::Zero
        [Win32Notifier]::SendMessageTimeout(
            [IntPtr]0xffff, # HWND_BROADCAST
            0x001A,         # WM_SETTINGCHANGE
            [UIntPtr]::Zero,
            "Environment",
            2,              # SMTO_ABORTIFHUNG
            3000,
            [ref]$result
        ) | Out-Null
    } catch {
        # 广播非阻断降级
    }
}

try {
    Write-Host ""
    Write-Host "🥕 cworker (cw) - Windows Native Installer" -ForegroundColor Green
    Write-Host "-------------------------------------------" -ForegroundColor Gray

    # 1. 确定部署目标路径（遵循单一事实来源 SSOT：%USERPROFILE%\.cworker\bin\cw.exe）
    $binDir = Join-Path $env:USERPROFILE ".cworker\bin"
    $targetExe = Join-Path $binDir "cw.exe"
    $tempExe = Join-Path $binDir "cw.exe.tmp"
    $sha256File = Join-Path $binDir "cw.exe.sha256"

    if (-not (Test-Path $binDir)) {
        New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    }

    # 2. 构造下载链接
    $repo = "crthu/cworker"
    $baseDownloadUrl = ""
    $cleanVer = $Version.Trim()

    if ($cleanVer -eq "latest" -or $cleanVer -eq "") {
        $baseDownloadUrl = "https://github.com/$repo/releases/latest/download"
    } else {
        if (-not $cleanVer.StartsWith("v") -and -not $cleanVer.StartsWith("V")) {
            $cleanVer = "v$cleanVer"
        }
        $baseDownloadUrl = "https://github.com/$repo/releases/download/$cleanVer"
    }

    $candidateMirrors = @()
    if ($Mirror) {
        $candidateMirrors += $Mirror.TrimEnd('/')
    } else {
        # 默认自愈候选池：优先直连（自动继承系统代理/海外环境），失败时自动平滑降级至高速镜像
        $candidateMirrors += ""
        $candidateMirrors += "https://ghfast.top"
        $candidateMirrors += "https://mirror.ghproxy.com"
    }

    $webRequestArgs = @{
        UseBasicParsing = $true
        TimeoutSec      = 15
    }
    if ($Proxy) {
        $webRequestArgs["Proxy"] = $Proxy
    }

    # 3. 自动探测下载（具备自动降级自愈能力）
    $downloadSuccess = $false
    $lastError = $null

    foreach ($m in $candidateMirrors) {
        $binaryUrl = if ($m -eq "") { "$baseDownloadUrl/cw.exe" } else { "$m/$baseDownloadUrl/cw.exe" }
        $hashUrl = if ($m -eq "") { "$baseDownloadUrl/cw.exe.sha256" } else { "$m/$baseDownloadUrl/cw.exe.sha256" }
        $nodeDesc = if ($m -eq "") { "GitHub Official (Direct / System Proxy)" } else { $m }

        try {
            Write-Step "Trying channel: $nodeDesc ..."

            # 尝试拉取校验和
            try {
                Invoke-WebRequest @webRequestArgs -Uri $hashUrl -OutFile $sha256File -ErrorAction Stop
            } catch {
                # 校验和文件非致命
            }

            # 拉取二进制文件
            Invoke-WebRequest @webRequestArgs -Uri $binaryUrl -OutFile $tempExe -ErrorAction Stop

            $downloadSuccess = $true
            break
        } catch {
            $lastError = $_
            Write-Warn "Channel '$nodeDesc' failed ($($_.Exception.Message)). Trying next fallback channel..."
            Remove-Item -Force -Path $tempExe -ErrorAction SilentlyContinue
            Remove-Item -Force -Path $sha256File -ErrorAction SilentlyContinue
        }
    }

    if (-not $downloadSuccess) {
        throw "All download channels failed. Last error: $lastError"
    }

    # 4. SHA-256 完整性核验
    if (Test-Path $sha256File) {
        $shaContent = Get-Content -Path $sha256File -Raw
        $expectedHash = ($shaContent -split '\s+')[0].Trim().ToLower()
        if ($expectedHash -match '^[a-f0-9]{64}$') {
            $actualHash = (Get-FileHash -Algorithm SHA256 -Path $tempExe).Hash.ToLower()
            if ($actualHash -ne $expectedHash) {
                Remove-Item -Force -Path $tempExe -ErrorAction SilentlyContinue
                Remove-Item -Force -Path $sha256File -ErrorAction SilentlyContinue
                throw "SHA-256 verification failed!`nExpected: $expectedHash`nActual:   $actualHash"
            }
            Write-Success "SHA-256 checksum verified ($actualHash)"
        }
        Remove-Item -Force -Path $sha256File -ErrorAction SilentlyContinue
    }

    # 尝试清理旧的 .old 临时残留（若先前占用该文件的服务或进程已释放）
    Get-ChildItem -Path $binDir -Filter "cw.old.*.exe" -ErrorAction SilentlyContinue | ForEach-Object {
        Remove-Item -Force -Path $_.FullName -ErrorAction SilentlyContinue
    }

    # 5. 原子替换安装（Rename-Replace 模式：使用唯一 GUID 规避运行中文件锁死）
    if (Test-Path $targetExe) {
        $oldExe = Join-Path $binDir "cw.old.$([System.Guid]::NewGuid().ToString('N').Substring(0,8)).exe"
        try {
            Move-Item -Force -Path $targetExe -Destination $oldExe -ErrorAction Stop
        } catch {
            if (-not $Force) {
                Remove-Item -Force -Path $tempExe -ErrorAction SilentlyContinue
                throw "Existing cw.exe could not be moved (running process lock). Stop the service first or run with -Force: $_"
            }
        }
    }
    Move-Item -Force -Path $tempExe -Destination $targetExe

    Write-Success "Installed binary to: $targetExe"

    # 6. 配置当前用户 PATH 环境变量（防重复追加）
    if (-not $NoPath) {
        $normBinDir = $binDir.TrimEnd('\')
        $regKey = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey("Environment", $true)
        $userPath = $regKey.GetValue("Path", "", [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)

        $pathList = $userPath -split ';' | Where-Object { $_ -ne "" }
        $alreadyInPath = $false
        foreach ($p in $pathList) {
            if ($p.Trim().TrimEnd('\').Equals($normBinDir, [System.StringComparison]::OrdinalIgnoreCase)) {
                $alreadyInPath = $true
                break
            }
        }

        if (-not $alreadyInPath) {
            $newPath = if ([string]::IsNullOrWhiteSpace($userPath)) {
                $binDir
            } elseif ($userPath.EndsWith(';')) {
                "$userPath$binDir"
            } else {
                "$userPath;$binDir"
            }
            $regKey.SetValue("Path", $newPath, [Microsoft.Win32.RegistryValueKind]::ExpandString)
            Write-Success "Added '$binDir' to User PATH environment variable."
            Broadcast-EnvChange
        } else {
            Write-Success "'$binDir' is already in User PATH."
        }
        $regKey.Close()

        # 同步更新当前会话的 $env:Path，实现终端无缝可用
        if (-not ($env:Path -split ';' | Where-Object { $_.Trim().TrimEnd('\').Equals($normBinDir, [System.StringComparison]::OrdinalIgnoreCase) })) {
            $env:Path = "$binDir;$env:Path"
        }
    }

    # 7. 引导完成
    Write-Host ""
    Write-Success "cworker (cw) installation complete!"
    Write-Host ""
    Write-Host "Next Steps:" -ForegroundColor Cyan
    Write-Host "  1. Verify installation:  cw version" -ForegroundColor White
    Write-Host "  2. Explore commands:     cw --help" -ForegroundColor White
    Write-Host "  3. (Optional) Register as Windows Service (Admin required):" -ForegroundColor White
    Write-Host "     cw service install && cw service start" -ForegroundColor DarkGray
    Write-Host ""

} catch {
    Write-Failure "Installation failed: $_"
    exit 1
}
