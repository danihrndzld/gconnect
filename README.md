gconnect is an unofficial Go client for the private Garmin Connect API. It is not affiliated with, endorsed by, or supported by Garmin Ltd. Garmin and Garmin Connect are trademarks of Garmin Ltd. or its subsidiaries.

# gconnect

gconnect signs in to Garmin Connect the way the Garmin Connect mobile app does, keeps the login fresh, and fetches your health data as raw JSON. It uses only the Go standard library. It never reads or writes files and never reads environment variables: you decide where the tokens live. It also ships `gconnecttest`, a fake Garmin server you can run your own tests against.

The sign in flows were last checked against the live service on 2026-10-14. Garmin can change or block this private API at any time, and this library may stop working without notice.

## Status

| Feature | State |
| --- | --- |
| Sign in with the mobile (DI) flow | Supported. This is the flow the Android and iOS apps use. |
| Older web sign in (widget and OAuth1) | Supported. It is tried once, only when the mobile flow is blocked. |
| Token refresh | Supported for both kinds of login. |
| Two step verification (MFA) | Not supported. `Login` returns the code `MFARequired`. |
| Endpoints | 38 named endpoints: daily metrics, date ranges, activity splits, activity lists, goals and the profile. |
| Go version | 1.24 or newer. |
| Dependencies | None outside the standard library. |

## Install

```sh
go get github.com/danihrndzld/gconnect
```

## Quick start

This program signs in once, saves the tokens to a file you choose, and prints yesterday's daily summary. On later runs, load the saved tokens instead of calling `Login` again.

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/danihrndzld/gconnect"
)

func main() {
	ctx := context.Background()
	auth := &gconnect.Auth{} // the zero value talks to the real Garmin

	tokens, err := auth.Login(ctx, os.Getenv("GARMIN_EMAIL"), os.Getenv("GARMIN_PASSWORD"))
	if err != nil {
		log.Fatal(err) // a *gconnect.LoginError; see "Sign in" below
	}
	save := func(t *gconnect.Tokens) error {
		b, err := json.Marshal(t)
		if err != nil {
			return err
		}
		return os.WriteFile("garmin-tokens.json", b, 0o600)
	}
	if err := save(tokens); err != nil {
		log.Fatal(err)
	}

	client := &gconnect.Client{Auth: auth, Tokens: tokens, OnTokens: save}
	name, err := client.Profile(ctx)
	if err != nil {
		log.Fatal(err)
	}
	stats, _ := gconnect.Lookup("stats")
	day := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	res := client.Get(ctx, stats, gconnect.Params{Date: day, DisplayName: name})
	if res.Code != "" {
		log.Fatal(res.Err)
	}
	fmt.Println(string(res.Data))
}
```

Store the tokens file with mode 0600. It holds the access and refresh tokens, which give full access to the account until they expire.

## Sign in

`Auth.Login` tries the mobile flow first: one JSON request to the SSO host, then a token request to `diauth.garmin.com`. If Garmin blocks that flow (a Cloudflare page, a captcha, an unexpected answer, or a 429 on the mobile login only), `Login` tries the older web widget flow once. It never retries after a wrong password, after an MFA prompt, or after a 429 from the token host.

Every failure is a `*gconnect.LoginError`. Its `Msg` never contains the password. The codes are:

| Code | Meaning | What to do |
| --- | --- | --- |
| `BadPassword` | Garmin rejected the email or the password. | Ask the user again. |
| `MFARequired` | The account uses two step verification. | This library cannot sign in to it. |
| `RateLimited` | Garmin answered 429. | Wait 15 minutes before any new sign in. |
| `Blocked` | Both flows were refused, usually with a 403. | Try later or from another network. |
| `LoginFailed` | A network error, an answer that is not JSON, or another unexpected answer. | Look at `Msg`. |

## Tokens and refresh

`Tokens` holds one login: `DI` for the mobile flow, or `OAuth1`, `OAuth2` and `Consumer` for the older flow. The JSON field names match the ones Garmin and the garth project use, so you can marshal the struct as it is.

`Auth.Fresh` refreshes the tokens when the access token expires within a margin you choose, and tells you whether it did. `Client` calls it before every request with a 15 minute margin, and calls `Refresh` once more after a 401. Each time it gets new tokens it calls `Client.OnTokens`, which is where you save them. If `OnTokens` returns an error, the client logs a warning with the code `SAVE_FAILED` and keeps going with the new tokens in memory.

`Refresh` returns `ErrSessionDead` only when Garmin proves the login is dead: a 400 or 401 from the DI refresh, a 401 from the older flow's token exchange, or a DI login with no refresh token. A network error, a 5xx, a 403 or a 429 never invalidates a saved login: `Refresh` returns a plain error, and you should keep the old tokens and try again later. `Fresh` and `Refresh` return `ErrNoTokens` when the tokens hold no complete login.

## Rate limits

Garmin limits sign ins per account. On 2026-10-14, after 4 sign ins within 40 minutes, the next one got a 429, and the account could not sign in for a while, in the official app too. Sign in once and keep the tokens. Never call `Login` in a loop or retry it automatically. After `RateLimited`, wait at least 15 minutes. The library does not keep a cooldown for you, because that needs storage; keep one in your program if users can press a sign in button repeatedly.

For data requests, `Client` follows these rules:

- It waits at least `Gap` between requests. The default is 500 ms. A negative `Gap` turns the pause off, which is only sensible against a local fake server.
- On a 429 it waits for `Retry-After` (seconds or an HTTP date, at most 60 s) or for the backoff, at most 2 times. A third 429 stops the run with `StopRateLimited`.
- On a network error or a timeout (30 s per attempt by default) it retries up to 5 times. The wait is `min(5000 * 2^attempt + jitter, 60000)` ms, with a jitter of 0 to 999 ms.
- On a 403 it skips that request and keeps the tokens. A 403 on the profile, or 5 in a row, stops the run with `StopBlocked`.
- On a 401 it refreshes once and retries. A second 401 stops the run with `StopSessionDead`.
- A 204 or an empty body is a success with no data.
- It reads at most 1 MB of an answer. A longer answer has the code `TOO_LARGE`.

Once a run has stopped, `Client.Stopped` returns the reason, and every later `Get` returns at once without sending anything.

## Results

`Get` returns a `Result`. `Data` holds the raw JSON bytes exactly as Garmin sent them, so you can decode them into your own types or store them unchanged. `Code` is empty on success. Otherwise it is one of `BAD_JSON`, `TOO_LARGE`, `TIMEOUT`, `NETWORK`, `AUTH`, `FORBIDDEN`, `RATE_LIMIT`, `HTTP_4XX` or `HTTP_5XX`, and `Err` describes the failure with any secrets removed. `Safe` is a shorter form for callers that skip what fails: it returns the data or nil, and logs the reason through `Client.Log`.

## Endpoints

`Daily()`, `Aggregated()`, `Activity()` and `List()` return the endpoint tables, and `Lookup(name)` finds one by name. Each call returns a copy you may change.

| Type | Params | Names |
| --- | --- | --- |
| daily | `Date`, and `DisplayName` for six of them | `stats`, `user_summary`, `heart_rates`, `rhr`, `hrv`, `stress`, `all_day_stress`, `sleep`, `body_battery`, `body_battery_events`, `respiration`, `spo2`, `steps`, `intensity_minutes`, `hydration`, `training_readiness`, `morning_training_readiness`, `training_status`, `weigh_ins`, `max_metrics`, `endurance_score`, `running_tolerance` |
| aggregated | `Start`, `End`, and `DisplayName` for `race_predictions` | `blood_pressure`, `agg_weigh_ins`, `daily_steps`, `weekly_steps`, `weekly_intensity`, `body_composition`, `progress_summary`, `race_predictions`, `lactate_threshold`, `cycling_ftp`, `fitness_age`, `hill_score` |
| activity | `ActivityID` | `activity_splits` |
| list | `Start`, `End`, `Offset`, `Limit` (0 means 20) for `activities_by_date` | `activities_by_date`, `goals`, `social_profile` |

`stats` and `user_summary` share one URL, as do `stress` and `all_day_stress`, and `training_readiness` and `morning_training_readiness`. The second name of each pair has `AliasOf` set to the first. Garmin accepts at most 28 days per `daily_steps` request, so split longer ranges yourself. `Client.Profile` returns the display name that six daily endpoints need.

## Testing your code with gconnecttest

`gconnecttest.New` starts a fake Garmin on a loopback port. It serves both sign in flows, the token refresh and connectapi, issues synthetic tokens, and answers 401 to any token it did not issue. You choose its behavior with `Set`, add your own data routes with `Handle`, and read the requests it got with `Calls`.

```go
func TestMyExport(t *testing.T) {
	srv := gconnecttest.New()
	defer srv.Close()
	srv.Handle("/hrv-service/hrv/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"hrvSummary":{"weeklyAvg":52}}`))
	})
	srv.Set(func(c *gconnecttest.Control) { c.FailOnce["/hrv-service/"] = 429 })

	auth := &gconnect.Auth{Hosts: srv.Hosts()}
	tokens, err := auth.Login(context.Background(), "test@example.invalid", "right-password")
	if err != nil {
		t.Fatal(err)
	}
	client := &gconnect.Client{Auth: auth, Tokens: tokens, Gap: -1}
	hrv, _ := gconnect.Lookup("hrv")
	res := client.Get(context.Background(), hrv, gconnect.Params{Date: "2026-10-07"})
	// res.Data is {"hrvSummary":{"weeklyAvg":52}} after one 429 and one retry.
}
```

A route that ends in `/` serves every path under it, and the longest match wins. `DataStatus` forces a status on every request whose path starts with a prefix, and `FailOnce` forces it once. When several prefixes match, the longest one wins. The other knobs choose the sign in outcome (`MobileLogin`, `DIAuth`, `Widget429`, `WidgetStatus`, `Password`), the refresh outcome (`RefreshFail`) and the token lifetime (`TokenTTL`).

The library's own tests can only dial loopback addresses: a `TestMain` guard refuses any other address, so no test can reach the real Garmin by mistake. Doing the same in your tests is a good idea.

## Security

- The library has no access to the disk or the environment. An architecture test checks that it imports none of `os`, `os/exec`, `path/filepath`, `io/fs`, `syscall` or `unsafe` outside its tests.
- `Login` takes the password as an argument, sends it only to the SSO host, and never logs or stores it.
- Error texts pass through `Redact`, which hides Bearer and Basic values, cookies, JWT shaped strings, secret query values and JSON pairs with secret keys. You can call `Redact` and `RedactURL` on your own logs too.
- Never log the tokens or commit a tokens file. Treat them like a password.

## Legal and ethics

The Garmin Connect API used here is private and was reverse engineered. Garmin can change or block it at any time. You are responsible for following Garmin's terms of use for your account. Use the library for your own data, keep the request rate low, and do not sign in more often than you need to: a rate limited sign in also locks you out of the official app for a while.

The older sign in flow downloads its OAuth consumer key from `thegarth.s3.amazonaws.com`, a bucket run by the garth project, not by this library. Set `Hosts.Consumer` to your own copy if you do not want that run time dependency.

No real account data belongs in this repository. Test fixtures are written by hand, and the fake issues synthetic tokens only.

## Versioning

The library follows semantic versioning. While the version is v0, a minor release may change the API; read the release notes before you upgrade. A v1.0.0 will follow once the API has stayed the same through two releases of the app that uses it.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the checks, the test conventions and how to add an endpoint, and [SECURITY.md](SECURITY.md) to report a security problem. Changes are listed in [CHANGELOG.md](CHANGELOG.md).

## Credits

The sign in flows follow [matin/garth](https://github.com/matin/garth) and [cyberjunky/python-garminconnect](https://github.com/cyberjunky/python-garminconnect).

## License

MIT. See [LICENSE](LICENSE).
