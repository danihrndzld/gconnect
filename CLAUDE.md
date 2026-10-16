@AGENTS.md

## Claude Code notes

- AGENTS.md above is the single source of the rules. Change the rules there, never here.
- Before you call a task done, run `gofmt -l .`, `go vet ./...` and `go test -race -count=1 ./...`, and report the numbers.
- When a change needs the consumer app to follow, say which tag it must take; do not edit the app repository from here.
