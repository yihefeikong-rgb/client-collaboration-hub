param(
    [Parameter(Mandatory = $true)]
    [string]$Root
)

$ErrorActionPreference = 'Stop'
$Root = (Resolve-Path -LiteralPath $Root).Path

function Find-CollabExecutable {
    $local = Join-Path $Root 'collab.exe'
    if (Test-Path -LiteralPath $local -PathType Leaf) {
        return (Resolve-Path -LiteralPath $local).Path
    }
    $globalDir = Join-Path $env:LOCALAPPDATA 'Programs\client-collaboration-hub'
    $global = Join-Path $globalDir 'collab.exe'
    if (Test-Path -LiteralPath $global -PathType Leaf) {
        return (Resolve-Path -LiteralPath $global).Path
    }
    foreach ($candidate in @(Get-Command collab.exe -ErrorAction SilentlyContinue)) {
        return $candidate.Source
    }
    throw "未找到 collab 程序：$local（可将 collab.exe 放到本目录，或运行 scripts\install-global.ps1 全局安装）"
}

$executable = Find-CollabExecutable

# 校验 pid 文件/进程检查使用真实 exe 路径，避免同目录 symlink 干扰。
$executable = (Resolve-Path -LiteralPath $executable).Path

if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
    throw "未找到 collab 程序：$executable"
}

# 幂等检查：watch 已在运行时提示并退出，不重复启动进程。
foreach ($candidate in @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue)) {
    if ($candidate.ExecutablePath -ne $executable -or $candidate.CommandLine -notmatch '(^|\s)watch(\s|$)') {
        continue
    }
    Write-Output "collab watch 已在运行（PID $($candidate.ProcessId)），不重复启动。"
    exit 0
}

$logDir = Join-Path $Root 'logs'
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$stamp = Get-Date -Format 'yyyyMMdd_HHmmss'
$stdoutLog = Join-Path $logDir "watch-$stamp.out.log"
$stderrLog = Join-Path $logDir "watch-$stamp.err.log"

$process = Start-Process -FilePath $executable -ArgumentList 'watch' -WorkingDirectory $Root -WindowStyle Hidden -RedirectStandardOutput $stdoutLog -RedirectStandardError $stderrLog -PassThru

Start-Sleep -Milliseconds 500
if ($process.HasExited) {
    throw 'collab watch 启动后立即退出，请查看日志。'
}

Write-Output "collab watch 已后台启动。"
Write-Output "PID：$($process.Id)"
Write-Output "日志：$stdoutLog（错误输出：$stderrLog）"
