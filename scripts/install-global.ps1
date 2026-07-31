param(
    [switch]$SkipPath
)

$ErrorActionPreference = 'Stop'

$source = Join-Path $PSScriptRoot '..\collab.exe'
if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
    throw "未找到 collab.exe：$source（请先在仓库根目录执行 go build ./cmd/collab）"
}

$targetDir = Join-Path $env:LOCALAPPDATA 'Programs\client-collaboration-hub'
New-Item -ItemType Directory -Force -Path $targetDir | Out-Null
$target = Join-Path $targetDir 'collab.exe'
Copy-Item -LiteralPath $source -Destination $target -Force

if (-not $SkipPath) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -notlike "*$targetDir*") {
        $updated = if ([string]::IsNullOrWhiteSpace($userPath)) { $targetDir } else { "$userPath;$targetDir" }
        [Environment]::SetEnvironmentVariable('Path', $updated, 'User')
    }
}

Write-Output "collab 已全局安装：$target"
Write-Output "数据目录：$(Join-Path $env:LOCALAPPDATA 'client-collaboration-hub\collaboration')"
if ($SkipPath) {
    Write-Output "未修改用户 PATH；如需在任意目录直接运行 collab，请手动把 $targetDir 加入 PATH。"
} else {
    Write-Output "已加入用户 PATH。新开的终端可直接运行 collab；当前终端请先刷新环境变量。"
}
