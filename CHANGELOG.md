# Changelog

All notable changes to this module. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [semantic versioning](https://semver.org/). While the version is v0, a minor release may change the API.

## Unreleased

### Added

- `AGENTS.md`, `CLAUDE.md`, `CONTRIBUTING.md`, `SECURITY.md`, and a pull request template.
- `scripts/report/`, which rebuilds the PDF test report.
- `ids_test.go`, which fails when two tests share a technique ID.

## v0.1.1 (2026-10-16)

### Changed

- The README and the test comments describe the library on its own. Documentation only; no code changes.

## v0.1.0 (2026-10-16)

### Added

- `Auth`: DI mobile sign in, with one fallback to the widget and OAuth1 flow, `Refresh` and `Fresh`. `LoginError` carries the codes `BadPassword`, `MFARequired`, `RateLimited`, `Blocked` and `LoginFailed`.
- `Tokens`, with Garmin's field names for DI and legacy logins.
- `Client`: paced GET requests (500 ms by default), a refresh once on a 401, a stop after 5 refused requests in a row, retries on 429 that honor `Retry-After` (capped at 60 s), retries with backoff on network errors, `TOO_LARGE` above 1 MB, `OnTokens` and `Stopped`.
- 38 named endpoints, with `Lookup` and copy-returning accessors.
- `Redact` and `RedactURL`.
- `gconnecttest`, a fake Garmin server with sign in and refresh knobs, `Handle` for data routes, and a call log.
