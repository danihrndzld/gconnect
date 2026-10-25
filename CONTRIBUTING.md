# Contributing to gconnect

Thank you for helping. This library talks to a private, reverse-engineered API, so most of the rules below protect real accounts.

## Before you start

- You need Go 1.24 or newer. There are no other dependencies.
- Never paste real tokens, cookies, emails, display names or captured Garmin answers into an issue, a pull request or a test. Write small fixtures by hand.
- Do not run experiments against the real Garmin sign in. Garmin locks an account after about 4 sign ins in 40 minutes, and the lock also affects the official app.

## Checks

Run these before you open a pull request. CI runs the same checks on Linux and macOS with Go 1.24 and the current stable release.

```sh
gofmt -l .                      # must print nothing
go vet ./...
go test -race -count=1 ./...
go test -cover -coverpkg=./... -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1
```

Statement coverage must stay at or above 95 %. Today it is 99.8 %.

## Rules the tests enforce

- The library uses the standard library only, so `go.mod` has no `require` or `replace` line.
- Non-test files never import `os`, `os/exec`, `os/signal`, `path/filepath`, `io/fs`, `io/ioutil`, `syscall` or `unsafe`. The library never touches the disk or the environment.
- No test dials a non-loopback address.
- Every test ID is unique (`ids_test.go`).

## Writing tests

Each test or table row name starts with a technique ID followed by a short sentence that states the condition and the expected result:

| Prefix | Technique |
| --- | --- |
| `EP` | Equivalence partitioning: one representative per input class |
| `BVA` | Boundary values: the boundary and its neighbors |
| `DT-R` | Decision table: one rule per row |
| `ST-T`, `ST-I` | State transitions: valid ones, and invalid ones, one per test |
| `EG` | Error guessing: typical faults such as empty bodies, broken JSON or odd headers |
| `CL` | Checklist: shapes and contracts the README promises |
| `AC` | Acceptance: an end to end behavior |

Take the next free number for the prefix. Expected values come from documented behavior, never from the code under test. Use `gconnecttest` for every HTTP interaction, and inject `Sleep`, `Jitter` and `Now` instead of waiting on the clock.

## Adding an endpoint

1. Add one row to the right table in `endpoints.go`.
2. Add its expected URL to `jsURLs` in `endpoints_test.go`.
3. Update the count in the endpoint table test.
4. Add a `CHANGELOG.md` line under "Unreleased".

## Test report (optional)

`scripts/report/run.sh` runs the suite with the race detector and coverage, converts the output to JUnit XML and a coverage summary, writes `report.json`, and renders `report.html`, `report.pdf` and `summary.html` into `reportes-pruebas/<date>-gconnect/`, which is gitignored. It needs the art-of-cicd skill (github.com/danihrndzld/skills); set `ART_OF_CICD=/path/to/art-of-cicd` if it is not in `~/.claude/skills`. CI uploads the JUnit XML and the coverage summary as the `test-report` artifact.

## Pull requests

- Use one topic per pull request, with a conventional commit title (`feat(client): ...`, `fix(auth): ...`, `docs: ...`).
- Add a line to `CHANGELOG.md` under "Unreleased" for any change a user can notice.
- If you change an exported identifier, say how callers migrate.
