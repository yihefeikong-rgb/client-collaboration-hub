#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temp_root="$(mktemp -d "${TMPDIR:-/tmp}/collab-e2e.XXXXXX")"
cleanup() {
  case "$temp_root" in
    "${TMPDIR:-/tmp}"/collab-e2e.*) rm -rf -- "$temp_root" ;;
  esac
}
trap cleanup EXIT

binary="$temp_root/collab"
(cd "$repo_root" && go build -o "$binary" ./cmd/collab)
workspace="$temp_root/workspace"
project_path="$workspace/project"
mkdir -p "$project_path/changes" "$project_path/reports"
printf 'diff' > "$project_path/changes/fix.diff"
printf 'tests' > "$project_path/reports/test.txt"

run_json() {
  if ! (cd "$workspace" && "$binary" --json "$@" > "$temp_root/last.json"); then
    echo "collab $* failed" >&2
    return 1
  fi
  python3 - "$temp_root/last.json" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as stream:
    json.load(stream)
PY
}

run_json init
run_json client register --id codex --name Codex --capability create_task --capability review --capability import_export
run_json client register --id cc-haha --name CC-HAHA --capability execute --capability import_export
run_json project create --id project-1 --name Demo
run_json project bind --project project-1 --device device-1 --path "$project_path" --revision r1
run_json task create --id T-0001 --project project-1 --title "Binary workflow" --objective "Verify binary E2E" --acceptance "Tests pass" --creator codex
run_json task assign --task T-0001 --client cc-haha --expected-version 1
run_json task accept --task T-0001 --actor cc-haha --expected-version 2
run_json evidence add --task T-0001 --id E-diff --kind diff --summary Diff --created-by cc-haha --file-ref changes/fix.diff --expected-version 3
run_json evidence add --task T-0001 --id E-test --kind test --summary Tests --created-by cc-haha --file-ref reports/test.txt --expected-version 4
execution_package="$workspace/handoff-execution"
run_json handoff export --task T-0001 --client cc-haha --adapter manual-cc-haha --device device-1 --after-event 0 --output "$execution_package"
run_json response validate --package "$execution_package" --input "$execution_package/candidate-response.json"
run_json task submit --task T-0001 --actor cc-haha --evidence E-diff --evidence E-test --expected-version 5
review_package="$workspace/handoff-review"
run_json handoff export --task T-0001 --client codex --adapter manual-codex --device device-1 --after-event 0 --output "$review_package"
run_json review request-changes --task T-0001 --actor codex --body "Revise output" --expected-version 6
revision_package="$workspace/handoff-revision"
run_json handoff export --task T-0001 --client cc-haha --adapter manual-cc-haha --device device-1 --after-event 6 --output "$revision_package"
run_json task resume --task T-0001 --actor cc-haha --expected-version 7
run_json task submit --task T-0001 --actor cc-haha --evidence E-diff --evidence E-test --expected-version 8
run_json review approve --task T-0001 --actor codex --expected-version 9
run_json status --task T-0001 --device device-1
cp "$temp_root/last.json" "$temp_root/status.json"

python3 - "$execution_package" "$review_package" "$revision_package" "$project_path" "$temp_root/status.json" <<'PY'
import hashlib
import json
import pathlib
import sys

execution, review, revision, project, status_path = map(pathlib.Path, sys.argv[1:])
status = json.loads(status_path.read_text(encoding="utf-8"))
assert status["health"] == "HEALTHY"
assert status["state"]["status"] == "DONE"
assert status["binding_available"] is True
for package in (execution, review, revision):
    manifest_path = package / "manifest.json"
    handoff_path = package / "handoff.md"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    assert manifest["format_version"] == "1"
    assert manifest["package_id"].startswith("sha256:") and len(manifest["package_id"]) == 71
    assert (package / "candidate-response.json").is_file()
    assert (package / "candidate-response.schema.json").is_file()
    portable_text = handoff_path.read_text(encoding="utf-8") + manifest_path.read_text(encoding="utf-8")
    assert str(project) not in portable_text
expected = hashlib.sha256((project / "changes" / "fix.diff").read_bytes()).hexdigest()
execution_manifest = json.loads((execution / "manifest.json").read_text(encoding="utf-8"))
assert execution_manifest["evidence"][0]["files"][0]["sha256"] == expected
revision_manifest = json.loads((revision / "manifest.json").read_text(encoding="utf-8"))
assert [event["event_id"] for event in revision_manifest["events"]] == [7]
PY

echo "Binary CLI E2E passed: DONE with portable manual-cc-haha and manual-codex handoffs."
