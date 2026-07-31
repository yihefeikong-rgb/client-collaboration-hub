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

if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
    throw "未找到控制台程序：$executable"
}

$projectName = Split-Path -Leaf $Root
& $executable --json project register-local --path $Root --name $projectName | Out-Null
if ($LASTEXITCODE -ne 0) {
    throw '无法在全局协作中枢登记当前项目。'
}

function Test-ConsoleUrl([string]$CandidateUrl) {
    try {
        $response = Invoke-WebRequest -UseBasicParsing -Uri ($CandidateUrl + 'api/v1/overview') -TimeoutSec 1
        return $response.StatusCode -eq 200 -and $response.Headers['Content-Type'] -like 'application/json*'
    } catch {
        return $false
    }
}

function Find-RunningConsoleUrl {
	$updatedAt = (Get-Item -LiteralPath $executable).LastWriteTimeUtc
    foreach ($candidateProcess in @(Get-CimInstance Win32_Process -ErrorAction SilentlyContinue)) {
        if ($candidateProcess.ExecutablePath -ne $executable -or $candidateProcess.CommandLine -notmatch '(^|\s)ui(\s|$)') {
            continue
        }
        try {
            $running = Get-Process -Id $candidateProcess.ProcessId -ErrorAction Stop
            if ($running.StartTime.ToUniversalTime() -lt $updatedAt) {
                Stop-Process -Id $running.Id -Force -ErrorAction Stop
                continue
            }
        } catch {
            continue
        }
        foreach ($listener in @(Get-NetTCPConnection -OwningProcess $candidateProcess.ProcessId -State Listen -ErrorAction SilentlyContinue)) {
            $candidateUrl = "http://127.0.0.1:$($listener.LocalPort)/"
            if (Test-ConsoleUrl $candidateUrl) {
                return $candidateUrl
            }
        }
    }
}

$url = Find-RunningConsoleUrl
if (-not $url) {
    $process = Start-Process -FilePath $executable -ArgumentList 'ui' -WorkingDirectory $Root -WindowStyle Hidden -PassThru
    for ($attempt = 0; $attempt -lt 40; $attempt++) {
        foreach ($listener in @(Get-NetTCPConnection -OwningProcess $process.Id -State Listen -ErrorAction SilentlyContinue)) {
            $candidateUrl = "http://127.0.0.1:$($listener.LocalPort)/"
            if (Test-ConsoleUrl $candidateUrl) {
                $url = $candidateUrl
                break
            }
        }
        if ($url) {
            break
        }
        if ($process.HasExited) {
            throw '控制台进程启动后立即退出。'
        }
        Start-Sleep -Milliseconds 250
    }
}

if (-not $url) {
    throw '等待本机控制台响应超时。'
}

Start-Process $url
Write-Output "网页控制台已打开：$url"
