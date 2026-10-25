# Changelog

All notable changes to this module. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [semantic versioning](https://semver.org/). While the version is v0, a minor release may change the API.

## Unreleased

## v0.2.0 (2026-10-24)

### Added

- The official Garmin Personal API. `Auth.ExchangeAPIKey` trades a key from connect.garmin.com/app/settings/personalApiToken for a 24 hour access token; `Tokens.Personal` and `KindPersonal` hold it; `Refresh` and `Fresh` buy the next token with the key; `Client.Get` reads from `Hosts.PersonalAPI` (default `https://personal.connectapi.garmin.com`) for such tokens; `Headers` sends only the Bearer token.
- `PersonalRanges()`: `personal_daily_summaries`, `personal_sleep`, `personal_hrv`, `personal_body_battery` and `personal_fitness_age`, the paths the Personal API spells differently from connectapi. `PersonalTokenPath` names the token path.
- `Redact` hides `ApiKey` header values.
- `gconnecttest`: the token route (`Control.APIKey`, default `right-api-key`, and `Control.APIKeyStatus`), and a Personal API token gets 404 outside the paths the real Personal API answers.

### Changed

- `Hosts` has a fifth field, `PersonalAPI`. A positional literal `Hosts{a, b, c, d}` no longer compiles; use field names or `SameHost`.
- `Kind` answers `KindPersonal` when `Tokens.Personal` holds a key, even next to another login.

## v0.1.2 (2026-10-16)

### Added

- `IsSecretKey`, the key test that `Redact` and `RedactURL` use, exported so callers can scrub their own key and value pairs.
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
