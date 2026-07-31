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
    $previousCollabHome = $env:COLLAB_HOME
    $env:COLLAB_HOME = $workspace

    Push-Location $workspace
    try {
        Invoke-CollabJson @("init") | Out-Null
        Invoke-CollabJson @("project", "create", "--id", "project-1", "--name", "Demo") | Out-Null
        Invoke-CollabJson @("project", "bind", "--project", "project-1", "--device", "device-1", "--path", $projectPath, "--revision", "r1") | Out-Null
        Invoke-CollabJson @("task", "create", "--id", "T-0001", "--project", "project-1", "--title", "Binary workflow", "--objective", "Verify binary E2E", "--acceptance", "Tests pass", "--creator", "codex") | Out-Null
        Invoke-CollabJson @("task", "assign", "--task", "T-0001", "--client", "cc-haha", "--expected-version", "1") | Out-Null
        Invoke-CollabJson @("task", "accept", "--task", "T-0001", "--actor", "cc-haha", "--expected-version", "2") | Out-Null
        Invoke-CollabJson @("evidence", "add", "--task", "T-0001", "--id", "E-diff", "--kind", "diff", "--summary", "Diff", "--created-by", "cc-haha", "--file-ref", "changes/fix.diff", "--expected-version", "3") | Out-Null
        Invoke-CollabJson @("evidence", "add", "--task", "T-0001", "--id", "E-test", "--kind", "test", "--summary", "Tests", "--created-by", "cc-haha", "--file-ref", "reports/test.txt", "--expected-version", "4") | Out-Null
        $executionPackage = Join-Path $workspace "handoff-execution"
        Invoke-CollabJson @("handoff", "export", "--task", "T-0001", "--client", "cc-haha", "--adapter", "manual-cc-haha", "--device", "device-1", "--after-event", "0", "--output", $executionPackage) | Out-Null
        $executionResponse = Join-Path $workspace "candidate-response.cc-haha.json"
        $candidate = Get-Content -LiteralPath (Join-Path $executionPackage "candidate-response.json") -Raw | ConvertFrom-Json -ErrorAction Stop
        $candidate.proposed_action = "submit"
        $candidate.evidence_refs = @("E-candidate-diff", "E-candidate-test")
        $candidate.evidence = @(
            [pscustomobject]@{ id = "E-candidate-diff"; kind = "diff"; summary = "Candidate diff"; file_refs = @("changes/fix.diff") },
            [pscustomobject]@{ id = "E-candidate-test"; kind = "test"; summary = "Candidate tests"; file_refs = @("reports/test.txt") }
        )
        $candidate | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $executionResponse -NoNewline
        $response = Invoke-CollabJson @("response", "validate", "--package", $executionPackage, "--input", $executionResponse)
        if ($response.Value.steps.Count -ne 3 -or $response.Value.steps[0].program -ne "collab" -or -not ($response.Value.steps[0].args -contains "E-candidate-diff") -or -not ($response.Value.steps[1].args -contains "E-candidate-test") -or -not ($response.Value.steps[2].args -contains "submit")) {
            throw "candidate response did not produce the expected structured steps"
        }
        # The validator is read-only. The operator explicitly runs every reviewed step.
        Invoke-CollabJson @("evidence", "add", "--task", "T-0001", "--id", "E-candidate-diff", "--kind", "diff", "--summary", "Candidate diff", "--created-by", "cc-haha", "--file-ref", "changes/fix.diff", "--expected-version", "5") | Out-Null
        Invoke-CollabJson @("evidence", "add", "--task", "T-0001", "--id", "E-candidate-test", "--kind", "test", "--summary", "Candidate tests", "--created-by", "cc-haha", "--file-ref", "reports/test.txt", "--expected-version", "6") | Out-Null
        Invoke-CollabJson @("task", "submit", "--task", "T-0001", "--actor", "cc-haha", "--evidence", "E-candidate-diff", "--evidence", "E-candidate-test", "--expected-version", "7") | Out-Null
        $reviewPackage = Join-Path $workspace "handoff-review"
        Invoke-CollabJson @("handoff", "export", "--task", "T-0001", "--client", "codex", "--adapter", "manual-codex", "--device", "device-1", "--after-event", "0", "--output", $reviewPackage) | Out-Null
        $reviewResponse = Join-Path $workspace "candidate-response.codex-review.json"
        $candidate = Get-Content -LiteralPath (Join-Path $reviewPackage "candidate-response.json") -Raw | ConvertFrom-Json -ErrorAction Stop
        $candidate.proposed_action = "request_changes"
        $candidate.feedback = "Revise output"
        $candidate | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $reviewResponse -NoNewline
        $response = Invoke-CollabJson @("response", "validate", "--package", $reviewPackage, "--input", $reviewResponse)
        if ($response.Value.steps.Count -ne 1 -or $response.Value.steps[0].args -notcontains "Revise output") {
            throw "request_changes response did not preserve feedback"
        }
        Invoke-CollabJson @("review", "request-changes", "--task", "T-0001", "--actor", "codex", "--body", "Revise output", "--expected-version", "8") | Out-Null
        $revisionPackage = Join-Path $workspace "handoff-revision"
        Invoke-CollabJson @("handoff", "export", "--task", "T-0001", "--client", "cc-haha", "--adapter", "manual-cc-haha", "--device", "device-1", "--after-event", "8", "--output", $revisionPackage) | Out-Null
        Invoke-CollabJson @("task", "resume", "--task", "T-0001", "--actor", "cc-haha", "--expected-version", "9") | Out-Null
        Invoke-CollabJson @("task", "submit", "--task", "T-0001", "--actor", "cc-haha", "--evidence", "E-candidate-diff", "--evidence", "E-candidate-test", "--expected-version", "10") | Out-Null
        $approvalPackage = Join-Path $workspace "handoff-approve"
        Invoke-CollabJson @("handoff", "export", "--task", "T-0001", "--client", "codex", "--adapter", "manual-codex", "--device", "device-1", "--after-event", "10", "--output", $approvalPackage) | Out-Null
        $approvalResponse = Join-Path $workspace "candidate-response.codex-approve.json"
        $candidate = Get-Content -LiteralPath (Join-Path $approvalPackage "candidate-response.json") -Raw | ConvertFrom-Json -ErrorAction Stop
        $candidate.proposed_action = "approve"
        $candidate | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $approvalResponse -NoNewline
        $response = Invoke-CollabJson @("response", "validate", "--package", $approvalPackage, "--input", $approvalResponse)
        if ($response.Value.steps.Count -ne 1 -or $response.Value.steps[0].args -notcontains "approve") {
            throw "approve response did not produce an approval step"
        }
        Invoke-CollabJson @("review", "approve", "--task", "T-0001", "--actor", "codex", "--expected-version", "11") | Out-Null
        $status = Invoke-CollabJson @("status", "--task", "T-0001", "--device", "device-1")
    } finally {
        Pop-Location
        $env:COLLAB_HOME = $previousCollabHome
    }

    if ($status.Value.health -ne "HEALTHY" -or $status.Value.state.status -ne "DONE" -or -not $status.Value.binding_available) {
        throw "unexpected final status: $($status.Text)"
    }
    foreach ($packagePath in @($executionPackage, $reviewPackage, $revisionPackage, $approvalPackage)) {
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
        if ($manifest.package_id -notmatch '^sha256:[0-9a-f]{64}$' -or -not (Test-Path -LiteralPath (Join-Path $packagePath "candidate-response.json")) -or -not (Test-Path -LiteralPath (Join-Path $packagePath "candidate-response.schema.json"))) {
            throw "incomplete portable package: $packagePath"
        }
    }
    $expectedHash = (Get-FileHash -LiteralPath (Join-Path $projectPath "changes\fix.diff") -Algorithm SHA256).Hash.ToLowerInvariant()
    $executionManifest = Get-Content -LiteralPath (Join-Path $executionPackage "manifest.json") -Raw | ConvertFrom-Json -ErrorAction Stop
    if ($executionManifest.evidence[0].files[0].sha256 -ne $expectedHash) {
        throw "evidence hash mismatch"
    }
    $revisionManifest = Get-Content -LiteralPath (Join-Path $revisionPackage "manifest.json") -Raw | ConvertFrom-Json -ErrorAction Stop
    if ($revisionManifest.events.Count -ne 1 -or $revisionManifest.events[0].event_id -ne 9) {
        throw "after-event cursor was not honored"
    }
    Write-Output "Binary CLI E2E passed: DONE with portable manual-cc-haha and manual-codex handoffs."
} finally {
    if (Test-Path -LiteralPath $tempRoot) {
        Remove-Item -LiteralPath $tempRoot -Recurse -Force
    }
}
