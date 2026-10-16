"""Turns `go test -json` output into JUnit XML and a Go cover profile into an istanbul
json-summary, the two inputs armar_reporte.py reads.

  python3 -I scripts/report/go_to_report_inputs.py junit  test.json  junit.xml
  python3 -I scripts/report/go_to_report_inputs.py cober  cover.out  coverage.json [module/]

JUnit holds leaf tests only: a test with subtests is a group, not a case. Go measures
statements, not branches, so every branch count is 0 of 0.
"""
import json
import sys
import xml.etree.ElementTree as ET
from collections import defaultdict


def junit(src, dst):
    tests, out = {}, defaultdict(list)
    for line in open(src):
        try:
            e = json.loads(line)
        except ValueError:
            continue
        name = e.get("Test")
        if not name:
            continue
        key = (e["Package"], name)
        if e["Action"] == "output":
            out[key].append(e.get("Output", ""))
        elif e["Action"] in ("pass", "fail", "skip"):
            tests[key] = (e["Action"], e.get("Elapsed", 0))
    parents = {(p, n.rsplit("/", 1)[0]) for p, n in tests if "/" in n}
    suite = ET.Element("testsuites")
    by_pkg = defaultdict(list)
    for (pkg, name), v in sorted(tests.items()):
        if (pkg, name) not in parents and name != "TestMain":
            by_pkg[pkg].append((name, v))
    for pkg, cases in by_pkg.items():
        s = ET.SubElement(suite, "testsuite", name=pkg, tests=str(len(cases)),
                          failures=str(sum(v[0] == "fail" for _, v in cases)))
        for name, (action, elapsed) in cases:
            tc = ET.SubElement(s, "testcase", classname=pkg, name=name, time=str(elapsed))
            if action == "fail":
                f = ET.SubElement(tc, "failure", message="fail")
                f.text = "".join(out[(pkg, name)])
            elif action == "skip":
                ET.SubElement(tc, "skipped")
    ET.ElementTree(suite).write(dst, encoding="utf-8", xml_declaration=True)
    print(f"{sum(len(c) for c in by_pkg.values())} leaf tests")


def cober(src, dst, prefix=""):
    blocks = {}
    for line in open(src):
        if line.startswith("mode:"):
            continue
        loc, n, count = line.rsplit(" ", 2)
        blocks[loc] = (int(n), max(blocks.get(loc, (0, 0))[1], int(count)))
    files = defaultdict(lambda: [0, 0])
    for loc, (n, count) in blocks.items():
        f = files[loc.split(":")[0].removeprefix(prefix)]
        f[1] += n
        f[0] += n if count else 0

    def entry(cov, tot):
        pct = round(100 * cov / tot, 2) if tot else 100
        return {"statements": {"total": tot, "covered": cov, "pct": pct},
                "branches": {"total": 0, "covered": 0, "pct": 100},
                "lines": {"total": tot, "covered": cov, "pct": pct},
                "functions": {"total": 0, "covered": 0, "pct": 100}}
    summary = {name: entry(*v) for name, v in sorted(files.items())}
    summary["total"] = entry(sum(v[0] for v in files.values()), sum(v[1] for v in files.values()))
    json.dump(summary, open(dst, "w"), indent=1)
    for name, e in summary.items():
        s = e["statements"]
        print(f"{name:32} {s['covered']:4}/{s['total']:<4} {s['pct']:6.2f}%")


if __name__ == "__main__":
    {"junit": junit, "cober": cober}[sys.argv[1]](*sys.argv[2:])
