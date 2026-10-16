# gconnect: instructions for coding agents

gconnect is an unofficial Go client for the private Garmin Connect API. It handles the DI mobile sign in with a one-time fallback to the widget and OAuth1 flow, token refresh, a paced HTTP client that knows Garmin's 401, 403, 429 and 204 rules, a table of 38 endpoints, and redaction. `gconnecttest` is a fake Garmin server that consumers test against. Human contributors read [CONTRIBUTING.md](CONTRIBUTING.md); this file holds the same rules plus what an agent needs in order to work safely.

## Hard rules

1. **Standard library only.** `go.mod` has no `require` and no `replace`. `arch_test.go` fails the build otherwise.
2. **No disk, no environment, no process.** Non-test files never import `os`, `os/exec`, `os/signal`, `path/filepath`, `io/fs`, `io/ioutil`, `syscall` or `unsafe`, and `arch_test.go` enforces this. The caller stores tokens and picks the hosts (`Hosts`), so the library cannot leak a token into a file.
3. **Never contact the real Garmin from code or tests.** A `TestMain` in each package refuses any non-loopback dial. Never run a live sign in, and above all never run one in a loop: Garmin answered 429 after about 4 sign ins in 40 minutes on 2026-10-14, and that limit also locks the account out of the official app.
4. **No real account data in the repository.** Write fixtures by hand. The fake issues synthetic tokens only, and real tokens, cookies, emails or captured Garmin answers never go into a commit, an issue or a log.
5. **The password is never logged, stored or put into an error.** Every error text that can carry a server answer goes through `Redact`.
6. **The token refresh contract is load-bearing.** `ErrSessionDead` (and `StopSessionDead` in `Client`) means only a 400 or 401 from the DI refresh grant, a 401 from the legacy OAuth2 exchange, or a second 401 on data after a successful refresh. A network error, a timeout, a 5xx, a 403 or a 429 keeps the tokens and returns a plain error. Consumers delete their saved login on `ErrSessionDead`, so a wrong classification logs users out.
7. **Exported API changes are deliberate.** While the module is at v0, a minor release may break the API, but every break gets a `CHANGELOG.md` entry under "Changed" with the migration. Raw `json.RawMessage` responses are intentional: typed decoding would lose Garmin's key order and number text.

## Layout

| File | What it holds |
| --- | --- |
| `doc.go` | The package documentation `go doc` shows. |
| `auth.go` | `Auth`: `Login` (DI first, widget once), `Refresh`, `Fresh`, OAuth1 signing, `Headers` (native Android headers for DI). |
| `tokens.go` | `Tokens` with Garmin's field names and `Kind`. |
| `client.go` | `Client`: pacing (`Gap`: 0 means 500 ms, a negative value disables it), retries, `OnTokens`, `Stopped`, the `TOO_LARGE` cap of 1 MB. |
| `endpoints.go` | The endpoint table and URL builders; `Lookup` and accessors return copies. |
| `redact.go` | `Redact`, `RedactURL`. |
| `gconnecttest/server.go` | The fake: `New`, `Hosts`, `Handle` (the longest prefix wins), `Set`, `Control` (a copy), `Calls`, `JWT`. |
| `arch_test.go`, `ids_test.go` | The architecture rules, and one owner per test ID. |
| `scripts/report/` | Builds the PDF test report (see CONTRIBUTING.md). |

## Commands

```sh
gofmt -l .                      # must print nothing
go vet ./...
go test -race -count=1 ./...
go test -cover -coverpkg=./... -coverprofile=cover.out ./... && go tool cover -func=cover.out | tail -1   # CI fails under 95 %
scripts/report/run.sh           # optional: the PDF test report in reportes-pruebas/ (gitignored)
```

CI (`.github/workflows/ci.yml`) runs gofmt, vet, the race tests and the 95 % statement coverage gate on Linux and macOS, with Go 1.24 and stable.

## Tests

- Write black-box tests. Expected values come from documented Garmin behavior, from the README contract or from an independent reference, never from reading the code under test.
- Every test or table row name starts with its technique ID: `EP` (equivalence partitions), `BVA` (boundaries), `DT-R` (decision table rules), `ST-T` / `ST-I` (valid and invalid state transitions), `EG` (error guessing), `CL` (checklist), `AC` (acceptance). Example: `t.Run("BVA-448 a Retry-After of 61 s is capped at 60 s", ...)`.
- Every ID is unique in the repository, and `ids_test.go` fails on a duplicate. Take the next free number for the prefix: `grep -ohE '"EP-[0-9]+ ' *_test.go gconnecttest/*_test.go | sort -t- -k2 -n | tail -1`.
- Write table tests with `t.Run`. Use `t.Parallel()` where the test changes no shared state. Tests that change `Control` use `srv.Set(func(c *gconnecttest.Control) {...})`.
- Inject time and sleep: `Client.Sleep`, `Client.Jitter` and `Auth.Now`. Never assert on wall-clock timing.
- Add an endpoint with one row in `endpoints.go`, its expected URL in `jsURLs` (`endpoints_test.go`), and the updated table count.

## Releases

1. Move the "Unreleased" items in `CHANGELOG.md` under the new version and date.
2. Commit, then run `git tag -a vX.Y.Z -m "vX.Y.Z: <one line>" && git push origin main vX.Y.Z`.
3. Wait for CI on the tag: `gh run watch`.
4. Check the module proxy: `GOPROXY=https://proxy.golang.org go list -m github.com/danihrndzld/gconnect@vX.Y.Z`.
5. The known consumer is the Garmin Export app (`danihrndzld/garmin-health-exporter`, `tauri-app/core`). It pins a tag with `go get github.com/danihrndzld/gconnect@vX.Y.Z && go mod tidy`. For local work on both repositories, use a gitignored `go.work`. Never commit a `replace`.

## Writing

Follow these rules in the README, doc comments and error texts: complete sentences, no dashes as punctuation, no jargon without a definition, and facts with numbers. Error texts never name a consumer app. The app adds its own wording.

## Commits

Use conventional commits (`feat(...)`, `fix(...)`, `test(...)`, `docs(...)`, `chore(...)`). The author and committer are `Daniel Hernández <danisnowman@gmail.com>`. Never add a Co-Authored-By trailer or any AI attribution.
