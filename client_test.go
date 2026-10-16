package gconnect_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/danihrndzld/garmin-health-exporter/tauri-app/core/gconnect"
	"github.com/danihrndzld/garmin-health-exporter/tauri-app/core/gconnect/gconnecttest"
)

type logLine struct{ kind, msg, code string }

// recorder collects what the client logs, sleeps and saves, so no test waits in real time.
type recorder struct {
	mu    sync.Mutex
	logs  []logLine
	waits []time.Duration
	saved []*gconnect.Tokens
}

func (r *recorder) has(kind, prefix, code string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.logs {
		if l.kind == kind && strings.HasPrefix(l.msg, prefix) && l.code == code {
			return true
		}
	}
	return false
}

func (r *recorder) any(sub string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.logs {
		if strings.Contains(l.msg, sub) {
			return true
		}
	}
	return false
}

// newClient wires a client to srv with tok. Gap -1 turns the pause off, so it never shows up
// in the recorded waits.
func newClient(srv *gconnecttest.Server, tok *gconnect.Tokens) (*gconnect.Client, *recorder) {
	r := &recorder{}
	c := &gconnect.Client{
		Auth:   &gconnect.Auth{Hosts: srv.Hosts()},
		Tokens: tok,
		Gap:    -1,
		Sleep: func(_ context.Context, d time.Duration) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.waits = append(r.waits, d)
			return nil
		},
		Jitter: func() time.Duration { return 0 },
		Log: func(kind, msg, code string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.logs = append(r.logs, logLine{kind, msg, code})
		},
		OnTokens: func(t *gconnect.Tokens) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.saved = append(r.saved, t)
			return nil
		},
	}
	return c, r
}

// signedIn signs in to the fake (DI unless set says otherwise) and returns a client.
func signedIn(t *testing.T, set func(*gconnecttest.Control)) (*gconnecttest.Server, *gconnect.Client, *recorder) {
	t.Helper()
	srv := fakeGarmin(t)
	if set != nil {
		srv.Set(set)
	}
	tok, err := (&gconnect.Auth{Hosts: srv.Hosts()}).Login(context.Background(), "e@x", "right-password")
	if err != nil {
		t.Fatal(err)
	}
	srv.Reset()
	c, r := newClient(srv, tok)
	return srv, c, r
}

func ep(name string) gconnect.Endpoint {
	e, ok := gconnect.Lookup(name)
	if !ok {
		panic(name)
	}
	return e
}

const day = "2026-10-07"

var hrvPath = "/hrv-service/hrv/" + day

func TestClientStatusRules(t *testing.T) {
	ctx := context.Background()
	social := "/userprofile-service/socialProfile"

	t.Run("EP-331 ports EP-01: HTTP 204 is success with no data, logged as [no data] and not as a warning", func(t *testing.T) {
		srv, c, r := signedIn(t, nil)
		srv.Handle(hrvPath, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
		if data := c.Safe(ctx, ep("hrv"), gconnect.Params{Date: day}); data != nil {
			t.Fatalf("data = %s", data)
		}
		if !r.has("dim", "[no data] hrv (HTTP 204)", "") || r.any("[skip]") {
			t.Fatalf("logs = %v", r.logs)
		}
	})

	t.Run("EP-332 ports EP-02: one 401 refreshes the token and retries; the new tokens go to OnTokens", func(t *testing.T) {
		srv, c, r := signedIn(t, nil)
		before := c.Tokens
		srv.Set(func(k *gconnecttest.Control) { k.FailOnce[social] = 401 })
		if res := c.Get(ctx, ep("social_profile"), gconnect.Params{}); res.Code != "" {
			t.Fatalf("result = %+v", res)
		}
		if c.Stopped() != nil || len(r.saved) != 1 || c.Tokens == before || r.saved[0] != c.Tokens {
			t.Fatalf("stopped %v, saved %d, tokens replaced %v", c.Stopped(), len(r.saved), c.Tokens != before)
		}
		if n := len(srv.Calls(social)); n != 2 {
			t.Fatalf("%d profile calls, want 2", n)
		}
	})

	t.Run("EP-333 ports EP-03: 401 again after a refresh stops the run with StopSessionDead", func(t *testing.T) {
		srv, c, _ := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) { k.DataStatus[social] = 401 })
		res := c.Get(ctx, ep("social_profile"), gconnect.Params{})
		if res.Code != "AUTH" || c.Stopped() == nil || c.Stopped().Reason != gconnect.StopSessionDead {
			t.Fatalf("result %+v, stopped %v", res, c.Stopped())
		}
		// Every later request returns at once.
		if res := c.Get(ctx, ep("hrv"), gconnect.Params{Date: day}); res.Code != "AUTH" || len(srv.Calls(hrvPath)) != 0 {
			t.Fatalf("after the stop: %+v", res)
		}
	})

	t.Run("EP-334 ports EP-04: one 403 skips that endpoint only; tokens stay and the run goes on", func(t *testing.T) {
		srv, c, r := signedIn(t, nil)
		before := c.Tokens
		srv.Set(func(k *gconnecttest.Control) { k.FailOnce["/hrv-service/"] = 403 })
		if res := c.Get(ctx, ep("hrv"), gconnect.Params{Date: day}); res.Code != "FORBIDDEN" || res.Status != 403 {
			t.Fatalf("result = %+v", res)
		}
		if c.Stopped() != nil || c.Tokens != before || len(r.saved) != 0 {
			t.Fatal("a 403 touched the tokens or stopped the run")
		}
		if res := c.Get(ctx, ep("social_profile"), gconnect.Params{}); res.Code != "" {
			t.Fatalf("next request: %+v", res)
		}
	})

	for _, n := range []int{4, 5} {
		id := map[int]string{4: "BVA-331 ports BVA-01: four 403 answers in a row do not stop the run", 5: "BVA-332 ports BVA-02: the fifth 403 in a row stops the run with StopBlocked, tokens kept"}[n]
		t.Run(id, func(t *testing.T) {
			srv, c, r := signedIn(t, nil)
			srv.Set(func(k *gconnecttest.Control) { k.DataStatus["/hrv-service/"] = 403 })
			for range n {
				c.Get(ctx, ep("hrv"), gconnect.Params{Date: day})
			}
			stop := c.Stopped()
			if n == 4 && stop != nil {
				t.Fatalf("stopped after 4: %v", stop)
			}
			if n == 5 && (stop == nil || stop.Reason != gconnect.StopBlocked || !strings.Contains(stop.Msg, "refused 5 requests")) {
				t.Fatalf("stop = %+v", stop)
			}
			if len(r.saved) != 0 {
				t.Fatal("tokens were replaced")
			}
		})
	}

	t.Run("BVA-336 an answer that is not a 403 resets the streak: 4, one success, 4 more do not stop", func(t *testing.T) {
		srv, c, _ := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) { k.DataStatus["/hrv-service/"] = 403 })
		for i := range 9 {
			if i == 4 {
				c.Get(ctx, ep("social_profile"), gconnect.Params{})
				continue
			}
			c.Get(ctx, ep("hrv"), gconnect.Params{Date: day})
		}
		if c.Stopped() != nil {
			t.Fatalf("stopped = %v", c.Stopped())
		}
	})

	t.Run("BVA-333 ports BVA-03: a 429 that clears succeeds and waits Retry-After", func(t *testing.T) {
		srv, c, r := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) { k.FailOnce[social] = 429 })
		if res := c.Get(ctx, ep("social_profile"), gconnect.Params{}); res.Code != "" {
			t.Fatalf("result = %+v", res)
		}
		if !slices.Equal(r.waits, []time.Duration{time.Second}) {
			t.Fatalf("waits = %v", r.waits)
		}
	})

	t.Run("BVA-334 ports BVA-04: a third 429 (over the 2 retry budget) stops the run with StopRateLimited", func(t *testing.T) {
		srv, c, r := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) { k.DataStatus[social] = 429 })
		res := c.Get(ctx, ep("social_profile"), gconnect.Params{})
		if res.Code != "RATE_LIMIT" || c.Stopped() == nil || c.Stopped().Reason != gconnect.StopRateLimited {
			t.Fatalf("result %+v, stopped %v", res, c.Stopped())
		}
		if !slices.Equal(r.waits, []time.Duration{time.Second, time.Second}) || len(srv.Calls(social)) != 3 {
			t.Fatalf("waits %v, calls %d", r.waits, len(srv.Calls(social)))
		}
		if res := c.Get(ctx, ep("hrv"), gconnect.Params{Date: day}); res.Code != "RATE_LIMIT" || len(srv.Calls(hrvPath)) != 0 {
			t.Fatalf("after the stop: %+v", res)
		}
	})

	t.Run("BVA-335 a 429 that clears after the 2nd retry succeeds and waits Retry-After each time", func(t *testing.T) {
		srv, c, r := signedIn(t, nil)
		var mu sync.Mutex
		n := 0
		srv.Handle(hrvPath, func(w http.ResponseWriter, _ *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if n++; n <= 2 {
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(429)
				return
			}
			w.Write([]byte(`{"ok":1}`))
		})
		res := c.Get(ctx, ep("hrv"), gconnect.Params{Date: day})
		if res.Code != "" || string(res.Data) != `{"ok":1}` || !slices.Equal(r.waits, []time.Duration{3 * time.Second, 3 * time.Second}) {
			t.Fatalf("result %+v, waits %v", res, r.waits)
		}
	})

	t.Run("EG-332 ports EG-08: HTTP 403 on the profile stops right away, before any daily request", func(t *testing.T) {
		srv, c, _ := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) { k.DataStatus[social] = 403 })
		if _, err := c.Profile(ctx); err == nil {
			t.Fatal("Profile succeeded")
		}
		if c.Stopped() == nil || c.Stopped().Reason != gconnect.StopBlocked || !strings.HasPrefix(c.Stopped().Msg, "Garmin refused the profile request (HTTP 403).") {
			t.Fatalf("stopped = %v", c.Stopped())
		}
		c.Safe(ctx, ep("hrv"), gconnect.Params{Date: day})
		if n := len(srv.Calls(hrvPath)); n != 0 {
			t.Fatalf("%d hrv calls after the stop", n)
		}
	})
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		id, raw string
		want    time.Duration
		ok      bool
	}{
		{"EG-331 ports EG-01: an HTTP date is honored", now.Add(5 * time.Second).Format(http.TimeFormat), 5 * time.Second, true},
		{"EG-343 ports EG-01: seconds above 60 are capped at 60 s", "120", time.Minute, true},
		{"EG-344 ports EG-01: no header means no Retry-After", "", 0, false},
		{"EG-345 fractional seconds count, like Number() in JS", "1.5", 1500 * time.Millisecond, true},
		{"EG-346 a date in the past waits 0", now.Add(-time.Hour).Format(http.TimeFormat), 0, true},
		{"EG-347 garbage and Infinity are ignored", "Infinity", 0, false},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			got, ok := gconnect.RetryAfter(c.raw, now)
			if got != c.want || ok != c.ok {
				t.Fatalf("RetryAfter(%q) = %v %v", c.raw, got, ok)
			}
		})
	}
}

func TestClientHeaders(t *testing.T) {
	ctx := context.Background()
	t.Run("EG-333 DI requests carry every native header; legacy requests carry the mobile User-Agent", func(t *testing.T) {
		srv, c, _ := signedIn(t, nil)
		c.Get(ctx, ep("hrv"), gconnect.Params{Date: day})
		got := srv.Calls(hrvPath)[0].Header
		native := map[string]string{ // what the Android app sends (measured 2026-10-14)
			"User-Agent":                  "GCM-Android-5.23",
			"X-Garmin-User-Agent":         "com.garmin.android.apps.connectmobile/5.23; ; Google/sdk_gphone64_arm64/google; Android/33; Dalvik/2.1.0",
			"X-Garmin-Paired-App-Version": "10861",
			"X-Garmin-Client-Platform":    "Android",
			"X-App-Ver":                   "10861",
			"X-Lang":                      "en",
			"X-GCExperience":              "GC5",
			"Accept-Language":             "en-US,en;q=0.9",
		}
		for k, v := range native {
			if got.Get(k) != v {
				t.Errorf("%s = %q, want %q", k, got.Get(k), v)
			}
		}
		if got.Get("Authorization") != "Bearer "+c.Tokens.DI.AccessToken {
			t.Errorf("Authorization = %q", got.Get("Authorization"))
		}

		srv, c, _ = signedIn(t, func(k *gconnecttest.Control) { k.MobileLogin = "429" })
		if c.Tokens.Kind() != gconnect.KindLegacy {
			t.Fatal("not a legacy login")
		}
		c.Get(ctx, ep("hrv"), gconnect.Params{Date: day})
		if ua := srv.Calls(hrvPath)[0].Header.Get("User-Agent"); ua != "com.garmin.android.apps.connectmobile" {
			t.Errorf("legacy User-Agent = %q", ua)
		}
	})
	t.Run("EG-334 ports EG-03: goals are requested with start=1 and the Sec-Fetch-Site header", func(t *testing.T) {
		srv, c, _ := signedIn(t, nil)
		c.Get(ctx, ep("goals"), gconnect.Params{})
		calls := srv.Calls("/goal-service/goal/goals")
		if len(calls) != 1 || !strings.Contains(calls[0].Query, "start=1&") || calls[0].Header.Get("Sec-Fetch-Site") != "same-origin" {
			t.Fatalf("calls = %+v", calls)
		}
	})
}

func TestClientErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("EG-335 a 500 body comes out redacted in the [skip] line and cut to 200 characters", func(t *testing.T) {
		srv, c, r := signedIn(t, nil)
		srv.Handle(hrvPath, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"Bearer abc.def.ghi","password":"x"}` + strings.Repeat("é", 300)))
		})
		if c.Safe(ctx, ep("hrv"), gconnect.Params{Date: day}) != nil {
			t.Fatal("data on a 500")
		}
		var line logLine
		for _, l := range r.logs {
			if strings.HasPrefix(l.msg, "[skip] hrv: HTTP 500: ") {
				line = l
			}
		}
		preview := strings.TrimPrefix(line.msg, "[skip] hrv: HTTP 500: ")
		if line.kind != "warn" || line.code != "HTTP_5XX" || strings.Contains(preview, "abc.def") || strings.Contains(preview, `"x"`) {
			t.Fatalf("line = %+v", line)
		}
		if !strings.Contains(preview, "Bearer REDACTED") || utf8.RuneCountInString(preview) != 201 || !strings.HasSuffix(preview, "…") {
			t.Fatalf("preview (%d runes) = %s", utf8.RuneCountInString(preview), preview)
		}
	})

	t.Run("EG-336 Gap 0 means 500 ms between requests; a negative Gap means none", func(t *testing.T) {
		_, c, r := signedIn(t, nil)
		c.Gap = 0
		c.Get(ctx, ep("social_profile"), gconnect.Params{})
		c.Get(ctx, ep("social_profile"), gconnect.Params{})
		// The second request waits what is left of 500 ms; how much is left depends on the machine.
		if len(r.waits) != 1 || r.waits[0] <= 0 || r.waits[0] > 500*time.Millisecond {
			t.Fatalf("waits = %v", r.waits)
		}
	})

	backoff := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second}

	t.Run("EG-337 a timeout is retried 5 times with backoff min(5000*2^n + jitter, 60000) ms", func(t *testing.T) {
		srv, c, r := signedIn(t, nil)
		srv.Handle(hrvPath, func(_ http.ResponseWriter, req *http.Request) { <-req.Context().Done() })
		c.Timeout = 100 * time.Millisecond
		res := c.Get(ctx, ep("hrv"), gconnect.Params{Date: day})
		if res.Code != "TIMEOUT" || res.Err != "Timeout after 100ms" || !slices.Equal(r.waits, backoff) || len(srv.Calls(hrvPath)) == 0 {
			t.Fatalf("result %+v, waits %v, calls %d", res, r.waits, len(srv.Calls(hrvPath)))
		}
		if !r.has("dim", "[client] Timeout after 100ms on hrv on attempt 1, retrying in 5000ms", "") {
			t.Fatalf("logs = %v", r.logs)
		}
	})

	t.Run("EG-338 a network error is retried 5 times; jitter is added and the cap holds", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		dead := "http://" + l.Addr().String()
		l.Close()
		srv := fakeGarmin(t)
		c, r := newClient(srv, &gconnect.Tokens{DI: &gconnect.DI{AccessToken: "a", RefreshToken: "refresh-1", ExpiresAt: 1 << 40}})
		c.Auth.Hosts = gconnect.SameHost(dead)
		c.Jitter = func() time.Duration { return 999 * time.Millisecond }
		res := c.Get(ctx, ep("hrv"), gconnect.Params{Date: day})
		want := []time.Duration{5999 * time.Millisecond, 10999 * time.Millisecond, 20999 * time.Millisecond, 40999 * time.Millisecond, 60 * time.Second}
		if res.Code != "NETWORK" || !strings.HasPrefix(res.Err, "Network error: ") || !slices.Equal(r.waits, want) {
			t.Fatalf("result %+v, waits %v", res, r.waits)
		}
		if c.Stopped() != nil {
			t.Fatal("a network error stopped the run")
		}
	})

	t.Run("EG-342 a cancelled context returns at once without retries", func(t *testing.T) {
		_, c, r := signedIn(t, nil)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		res := c.Get(cctx, ep("hrv"), gconnect.Params{Date: day})
		if res.Code != "NETWORK" || len(r.waits) != 0 {
			t.Fatalf("result %+v, waits %v", res, r.waits)
		}
	})

	t.Run("CL-337 a 200 that is not JSON is BAD_JSON; a 404 is HTTP_4XX", func(t *testing.T) {
		srv, c, _ := signedIn(t, nil)
		srv.Handle(hrvPath, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<html>")) })
		if res := c.Get(ctx, ep("hrv"), gconnect.Params{Date: day}); res.Code != "BAD_JSON" || res.Status != 200 {
			t.Fatalf("result = %+v", res)
		}
		if res := c.Get(ctx, ep("stress"), gconnect.Params{Date: day}); res.Code != "HTTP_4XX" || res.Status != 404 {
			t.Fatalf("result = %+v", res)
		}
		if c.Stopped() != nil {
			t.Fatal("stopped")
		}
	})

	t.Run("EG-348 an answer over 1 MB is TOO_LARGE, not BAD_JSON; exactly 1 MB is read", func(t *testing.T) {
		srv, c, _ := signedIn(t, nil)
		big := func(n int) []byte { return []byte(`"` + strings.Repeat("a", n-2) + `"`) }
		srv.Handle(hrvPath, func(w http.ResponseWriter, _ *http.Request) { w.Write(big(1<<20 + 1)) })
		if res := c.Get(ctx, ep("hrv"), gconnect.Params{Date: day}); res.Code != "TOO_LARGE" || res.Data != nil || !strings.Contains(res.Err, "over 1 MB") {
			t.Fatalf("result = %+v", res.Err)
		}
		srv.Handle(hrvPath, func(w http.ResponseWriter, _ *http.Request) { w.Write(big(1 << 20)) })
		if res := c.Get(ctx, ep("hrv"), gconnect.Params{Date: day}); res.Code != "" || len(res.Data) != 1<<20 {
			t.Fatalf("1 MB answer: %q, %d bytes", res.Code, len(res.Data))
		}
	})

	t.Run("CL-358 GetURL extra headers replace the defaults whatever their case", func(t *testing.T) {
		srv, c, _ := signedIn(t, nil)
		c.GetURL(ctx, srv.URL()+hrvPath, http.Header{"user-agent": {"mine"}})
		if ua := srv.Calls(hrvPath)[0].Header.Values("User-Agent"); !slices.Equal(ua, []string{"mine"}) {
			t.Fatalf("User-Agent = %q", ua)
		}
	})

	t.Run("CL-338 Profile returns the display name", func(t *testing.T) {
		_, c, _ := signedIn(t, nil)
		if dn, err := c.Profile(ctx); err != nil || dn != "displayName-redacted" {
			t.Fatalf("Profile = %q %v", dn, err)
		}
	})
}

// expired is a DI login whose access token expired, so the next request refreshes it first.
func expired() *gconnect.Tokens {
	return &gconnect.Tokens{DI: &gconnect.DI{AccessToken: "old", RefreshToken: "refresh-1", ClientID: "GARMIN_CONNECT_MOBILE_ANDROID_DI_2025Q2", ExpiresAt: 1}}
}

func TestClientRefresh(t *testing.T) {
	ctx := context.Background()

	t.Run("EG-339 a 5xx on the refresh before a request stops with StopRefreshFailed and keeps the tokens", func(t *testing.T) {
		srv := fakeGarmin(t)
		srv.Set(func(k *gconnecttest.Control) { k.RefreshFail = 500 })
		tok := expired()
		c, r := newClient(srv, tok)
		res := c.Get(ctx, ep("hrv"), gconnect.Params{Date: day})
		if res.Code != "AUTH" || c.Stopped() == nil || c.Stopped().Reason != gconnect.StopRefreshFailed {
			t.Fatalf("result %+v, stopped %v", res, c.Stopped())
		}
		if strings.Count(strings.ToLower(c.Stopped().Msg), "refresh failed") != 1 {
			t.Errorf("message = %q", c.Stopped().Msg)
		}
		if c.Tokens != tok || c.Tokens.DI.AccessToken != "old" || len(r.saved) != 0 || len(srv.Calls(hrvPath)) != 0 {
			t.Fatal("the tokens changed or a data request went out")
		}
	})

	t.Run("EG-340 a 5xx on the refresh after a 401 stops with StopRefreshFailed and keeps the tokens", func(t *testing.T) {
		srv, c, r := signedIn(t, nil)
		before := c.Tokens
		srv.Set(func(k *gconnecttest.Control) {
			k.DataStatus["/hrv-service/"] = 401
			k.RefreshFail = 503
		})
		if res := c.Get(ctx, ep("hrv"), gconnect.Params{Date: day}); res.Code != "AUTH" {
			t.Fatalf("result = %+v", res)
		}
		if c.Stopped() == nil || c.Stopped().Reason != gconnect.StopRefreshFailed || c.Tokens != before || len(r.saved) != 0 {
			t.Fatalf("stopped %v, saved %d", c.Stopped(), len(r.saved))
		}
	})

	t.Run("EG-341 a 400 on the refresh before a request stops with StopSessionDead", func(t *testing.T) {
		srv := fakeGarmin(t)
		srv.Set(func(k *gconnecttest.Control) { k.RefreshFail = 400 })
		c, _ := newClient(srv, expired())
		c.Get(ctx, ep("hrv"), gconnect.Params{Date: day})
		if c.Stopped() == nil || c.Stopped().Reason != gconnect.StopSessionDead {
			t.Fatalf("stopped = %v", c.Stopped())
		}
	})

	t.Run("CL-335 an expired token is refreshed first and OnTokens gets the new tokens", func(t *testing.T) {
		srv := fakeGarmin(t)
		c, r := newClient(srv, expired())
		if res := c.Get(ctx, ep("social_profile"), gconnect.Params{}); res.Code != "" {
			t.Fatalf("result = %+v", res)
		}
		if len(r.saved) != 1 || r.saved[0].DI.AccessToken == "old" || c.Tokens != r.saved[0] {
			t.Fatal("the refreshed tokens did not reach OnTokens")
		}
	})

	t.Run("CL-336 OnTokens failing logs SAVE_FAILED and the run goes on with the new tokens", func(t *testing.T) {
		srv := fakeGarmin(t)
		c, r := newClient(srv, expired())
		c.OnTokens = func(*gconnect.Tokens) error { return errors.New("disk full") }
		if res := c.Get(ctx, ep("social_profile"), gconnect.Params{}); res.Code != "" {
			t.Fatalf("result = %+v", res)
		}
		if !r.has("warn", "", "SAVE_FAILED") || c.Tokens.DI.AccessToken == "old" || c.Stopped() != nil {
			t.Fatalf("logs %v, stopped %v", r.logs, c.Stopped())
		}
	})
}
