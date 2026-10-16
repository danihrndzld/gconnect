// Package gconnect is an unofficial client for the private Garmin Connect API. It is not
// affiliated with, endorsed by, or supported by Garmin Ltd.
//
// Auth signs in with the DI mobile flow (the one the Garmin Connect app uses) and falls back
// once to the older widget and OAuth1 flow. It returns Tokens and saves nothing: the package
// never touches the disk or the environment, so the caller stores the tokens and picks the
// hosts. Client sends paced GET requests to connectapi, refreshes the tokens when they near
// expiry or on a 401, retries 429 answers and network errors, and stops a run when Garmin
// proves the login dead or keeps refusing requests. Endpoint answers come back as raw JSON.
//
// Garmin limits sign ins per account (a 429 after about 4 sign ins in 40 minutes was
// measured on 2026-10-14). Never call Login in a loop; after RateLimited, wait 15 minutes.
//
// The gconnecttest package is a fake Garmin for tests.
package gconnect
