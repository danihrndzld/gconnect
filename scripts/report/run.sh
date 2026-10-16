#!/bin/sh
# Builds the PDF test report in reportes-pruebas/<date>-gconnect/ (gitignored).
# Needs uv and the testing-and-continuous-delivery skill (armar_reporte.py); see CONTRIBUTING.md.
set -eu
cd "$(dirname "$0")/../.."
R="reportes-pruebas/$(date +%F)-gconnect"
SKILL="${SKILL:-$HOME/.claude/skills/testing-and-continuous-delivery}"
mkdir -p "$R"
go test -race -count=1 -json -coverpkg=./... -coverprofile="$R/cover.out" ./... > "$R/test.json"
python3 -I scripts/report/go_to_report_inputs.py junit "$R/test.json" "$R/junit.xml"
python3 -I scripts/report/go_to_report_inputs.py cober "$R/cover.out" "$R/coverage.json" github.com/danihrndzld/gconnect/
python3 -I scripts/report/build_report_json.py "$R"
uv run "$SKILL/scripts/armar_reporte.py" "$R/reporte.json" --junit "$R/junit.xml" --cobertura "$R/coverage.json" --raiz .
echo "report: $R/reporte.pdf"
