## What changes

<!-- One topic. Say what a user of the library notices. -->

## Checks

- [ ] `gofmt -l .` prints nothing, and `go vet ./...` and `go test -race -count=1 ./...` pass
- [ ] Every new test name starts with a unique technique ID
- [ ] No real tokens, cookies, emails or captured Garmin answers anywhere in the diff
- [ ] `CHANGELOG.md` has a line under "Unreleased" (if a user can notice the change)
- [ ] Exported API changes say how callers migrate
