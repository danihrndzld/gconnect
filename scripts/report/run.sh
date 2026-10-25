#!/bin/sh
# Builds the test report in reportes-pruebas/<date>-gconnect/ (gitignored): JUnit XML, the
# coverage summary, report.json, and report.html, report.pdf and summary.html from the
# art-of-cicd skill's build_report.py (ART_OF_CICD=<skill dir>, default ~/.claude/skills/art-of-cicd).
set -eu
cd "$(dirname "$0")/../.."
R="reportes-pruebas/$(date +%F)-gconnect"
SKILL="${ART_OF_CICD:-$HOME/.claude/skills/art-of-cicd}"
mkdir -p "$R"
rc=0
go test -race -count=1 -json -coverpkg=./... -coverprofile="$R/cover.out" ./... > "$R/test.json" || rc=$?
python3 -I scripts/report/go_to_report_inputs.py junit "$R/test.json" "$R/junit.xml"
python3 -I scripts/report/go_to_report_inputs.py cober "$R/cover.out" "$R/coverage.json" github.com/danihrndzld/gconnect/
python3 -I - "$R" "$rc" <<'PY'
import json, subprocess, sys, datetime
r, rc = sys.argv[1], int(sys.argv[2])
git = lambda *a: subprocess.run(["git", *a], capture_output=True, text=True).stdout.strip()
cov = json.load(open(r + "/coverage.json"))["total"]["statements"]["pct"]
json.dump({
    "title": "gconnect test run", "skill": "art-of-cicd",
    "meta": {"date": datetime.date.today().isoformat(), "branch": git("rev-parse", "--abbrev-ref", "HEAD"),
             "commit": git("rev-parse", "--short", "HEAD"), "command": "scripts/report/run.sh",
             "tools": "go test -race -json, coverage over ./..."},
    "summary": f"<p>Black box tests against the gconnecttest fake, with the race detector. Statement coverage {cov} % (CI gate 95 %).</p>",
    "verdict": {"status": "pass" if rc == 0 and cov >= 95 else "fail",
                "text": f"go test exited {rc}; coverage {cov} % against the 95 % gate."},
}, open(r + "/report.json", "w"), indent=1)
PY
python3 "$SKILL/scripts/build_report.py" "$R/report.json" --junit "$R/junit.xml" --coverage "$R/coverage.json" --root . --formats html,pdf,summary --ci || true
echo "report: $R"
exit "$rc"
