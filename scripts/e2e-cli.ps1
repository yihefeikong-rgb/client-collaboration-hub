param(
    [string]$Binary = ""
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$tempRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("collab-e2e-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tempRoot | Out-Null

function Invoke-CollabJson {
    param([string[]]$CommandArgs)

    $output = & $script:binaryPath --json @CommandArgs 2>&1
    $exitCode = $LASTEXITCODE
    $text = [string]::Join([Environment]::NewLine, @($output | ForEach-Object { $_.ToString() }))
    if ($exitCode -ne 0) {
        throw "collab $($CommandArgs -join ' ') failed with exit code ${exitCode}: $text"
    }
    try {
        return @{ Text = $text; Value = ($text | ConvertFrom-Json -ErrorAction Stop) }
    } catch {
        throw "collab $($CommandArgs -join ' ') did not emit strict JSON: $text"
    }
}

try {
    if ([string]::IsNullOrWhiteSpace($Binary)) {
        $Binary = Join-Path $tempRoot "collab.exe"
        Push-Location $repoRoot
        try {
            & go build -o $Binary ./cmd/collab
            if ($LASTEXITCODE -ne 0) {
                throw "go build failed with exit code $LASTEXITCODE"
            }
        } finally {
            Pop-Location
        }
    }
    $script:binaryPath = (Resolve-Path -LiteralPath $Binary).Path
    $workspace = Join-Path $tempRoot "workspace"
    $projectPath = Join-Path $workspace "project"
    New-Item -ItemType Directory -Path (Join-Path $projectPath "changes") -Force | Out-Null
    New-Item -ItemType Directory -Path (Join-Path $projectPath "reports") -Force | Out-Null
    Set-Content -LiteralPath (Join-Path $projectPath "changes\fix.diff") -Value "diff" -NoNewline
    Set-Content -LiteralPath (Join-Path $projectPath "reports\test.txt") -Value "tests" -NoNewline

    Push-Location $workspace
    try {
        Invoke-CollabJson @("init") | Out-Null
        Invoke-CollabJson @("client", "register", "--id", "codex", "--name", "Codex", "--capability", "create_task", "--capability", "review") | Out-Null
        Invoke-CollabJson @("client", "register", "--id", "cc-haha", "--name", "CC-HAHA", "--capability", "execute") | Out-Null
        Invoke-CollabJson @("project", "create", "--id", "project-1", "--name", "Demo") | Out-Null
        Invoke-CollabJson @("project", "bind", "--project", "project-1", "--device", "device-1", "--path", $projectPath, "--revision", "r1") | Out-Null
        Invoke-CollabJson @("task", "create", "--id", "T-0001", "--project", "project-1", "--title", "Binary workflow", "--objective", "Verify binary E2E", "--acceptance", "Tests pass", "--creator", "codex") | Out-Null
        Invoke-CollabJson @("task", "assign", "--task", "T-0001", "--client", "cc-haha", "--expected-version", "1") | Out-Null
        Invoke-CollabJson @("task", "accept", "--task", "T-0001", "--actor", "cc-haha", "--expected-version", "2") | Out-Null
        Invoke-CollabJson @("evidence", "add", "--task", "T-0001", "--id", "E-diff", "--kind", "diff", "--summary", "Diff", "--created-by", "cc-haha", "--file-ref", "changes/fix.diff", "--expected-version", "3") | Out-Null
        Invoke-CollabJson @("evidence", "add", "--task", "T-0001", "--id", "E-test", "--kind", "test", "--summary", "Tests", "--created-by", "cc-haha", "--file-ref", "reports/test.txt", "--expected-version", "4") | Out-Null
        $executionPackage = Join-Path $workspace "handoff-execution"
        Invoke-CollabJson @("handoff", "export", "--task", "T-0001", "--client", "cc-haha", "--adapter", "manual-cc-haha", "--device", "device-1", "--after-event", "0", "--output", $executionPackage) | Out-Null
        Invoke-CollabJson @("task", "submit", "--task", "T-0001", "--actor", "cc-haha", "--evidence", "E-diff", "--evidence", "E-test", "--expected-version", "5") | Out-Null
        $reviewPackage = Join-Path $workspace "handoff-review"
        Invoke-CollabJson @("handoff", "export", "--task", "T-0001", "--client", "codex", "--adapter", "manual-codex", "--device", "device-1", "--after-event", "0", "--output", $reviewPackage) | Out-Null
        Invoke-CollabJson @("review", "request-changes", "--task", "T-0001", "--actor", "codex", "--body", "Revise output", "--expected-version", "6") | Out-Null
        $revisionPackage = Join-Path $workspace "handoff-revision"
        Invoke-CollabJson @("handoff", "export", "--task", "T-0001", "--client", "cc-haha", "--adapter", "manual-cc-haha", "--device", "device-1", "--after-event", "6", "--output", $revisionPackage) | Out-Null
        Invoke-CollabJson @("task", "resume", "--task", "T-0001", "--actor", "cc-haha", "--expected-version", "7") | Out-Null
        Invoke-CollabJson @("task", "submit", "--task", "T-0001", "--actor", "cc-haha", "--evidence", "E-diff", "--evidence", "E-test", "--expected-version", "8") | Out-Null
        Invoke-CollabJson @("review", "approve", "--task", "T-0001", "--actor", "codex", "--expected-version", "9") | Out-Null
        $status = Invoke-CollabJson @("status", "--task", "T-0001", "--device", "device-1")
    } finally {
        Pop-Location
    }

    if ($status.Value.health -ne "HEALTHY" -or $status.Value.state.status -ne "DONE" -or -not $status.Value.binding_available) {
        throw "unexpected final status: $($status.Text)"
    }
    foreach ($packagePath in @($executionPackage, $reviewPackage, $revisionPackage)) {
        $manifestPath = Join-Path $packagePath "manifest.json"
        $handoffPath = Join-Path $packagePath "handoff.md"
        $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json -ErrorAction Stop
        $handoff = Get-Content -LiteralPath $handoffPath -Raw
        if ($handoff.Contains($projectPath) -or (Get-Content -LiteralPath $manifestPath -Raw).Contains($projectPath)) {
            throw "local binding path leaked into $packagePath"
        }
        if ($manifest.format_version -ne "1") {
            throw "unexpected manifest format in $packagePath"
        }
    }
    $expectedHash = (Get-FileHash -LiteralPath (Join-Path $projectPath "changes\fix.diff") -Algorithm SHA256).Hash.ToLowerInvariant()
    $executionManifest = Get-Content -LiteralPath (Join-Path $executionPackage "manifest.json") -Raw | ConvertFrom-Json -ErrorAction Stop
    if ($executionManifest.evidence[0].files[0].sha256 -ne $expectedHash) {
        throw "evidence hash mismatch"
    }
    $revisionManifest = Get-Content -LiteralPath (Join-Path $revisionPackage "manifest.json") -Raw | ConvertFrom-Json -ErrorAction Stop
    if ($revisionManifest.events.Count -ne 1 -or $revisionManifest.events[0].event_id -ne 7) {
        throw "after-event cursor was not honored"
    }
    Write-Output "Binary CLI E2E passed: DONE with portable manual-cc-haha and manual-codex handoffs."
} finally {
    if (Test-Path -LiteralPath $tempRoot) {
        Remove-Item -LiteralPath $tempRoot -Recurse -Force
    }
}
