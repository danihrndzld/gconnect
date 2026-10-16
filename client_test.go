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

	"github.com/danihrndzld/gconnect"
	"github.com/danihrndzld/gconnect/gconnecttest"
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
	tok, err := (&gconnect.Auth{Hosts: srv.Hosts()}).Login(context.Background(), "e@x", password)
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

// deadURL is a loopback address nothing listens on: every request to it is a network error.
func deadURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return "http://" + l.Addr().String()
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

const day = "2026-10-07"

var (
	hrvPath = "/hrv-service/hrv/" + day
	social  = "/userprofile-service/socialProfile"
	hrv     = gconnect.Params{Date: day}
	ctx     = context.Background()
)

func TestClientStatusRules(t *testing.T) {
	t.Parallel()
	t.Run("EP-431 HTTP 204: success with no data, logged as [no data], not as a warning", func(t *testing.T) {
		t.Parallel()
		srv, c, r := signedIn(t, nil)
		srv.Handle(hrvPath, status(204))
		if data := c.Safe(ctx, ep("hrv"), hrv); data != nil {
			t.Fatalf("data = %s", data)
		}
		if !r.has("dim", "[no data] hrv (HTTP 204)", "") || r.any("[skip]") {
			t.Fatalf("logs = %v", r.logs)
		}
	})
	t.Run("EP-436 HTTP 200 with an empty body: success with no data", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		srv.Handle(hrvPath, status(200))
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "" || res.Data != nil || res.Status != 200 {
			t.Fatalf("result = %+v", res)
		}
	})
	t.Run("EP-432 one 401: refresh, retry, success; the new tokens go to OnTokens", func(t *testing.T) {
		t.Parallel()
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
	t.Run("EP-433 a 401 again after the refresh: AUTH, StopSessionDead, later requests not sent", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) { k.DataStatus[social] = 401 })
		res := c.Get(ctx, ep("social_profile"), gconnect.Params{})
		if res.Code != "AUTH" || c.Stopped() == nil || c.Stopped().Reason != gconnect.StopSessionDead || c.Stopped().Error() != c.Stopped().Msg {
			t.Fatalf("result %+v, stopped %v", res, c.Stopped())
		}
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "AUTH" || len(srv.Calls(hrvPath)) != 0 {
			t.Fatalf("after the stop: %+v", res)
		}
	})
	t.Run("EP-434 one 403: FORBIDDEN for that request only; tokens kept, the run goes on", func(t *testing.T) {
		t.Parallel()
		srv, c, r := signedIn(t, nil)
		before := c.Tokens
		srv.Set(func(k *gconnecttest.Control) { k.FailOnce["/hrv-service/"] = 403 })
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "FORBIDDEN" || res.Status != 403 {
			t.Fatalf("result = %+v", res)
		}
		if c.Stopped() != nil || c.Tokens != before || len(r.saved) != 0 {
			t.Fatal("a 403 touched the tokens or stopped the run")
		}
		if res := c.Get(ctx, ep("social_profile"), gconnect.Params{}); res.Code != "" {
			t.Fatalf("next request: %+v", res)
		}
	})
	t.Run("EP-435 a 503: HTTP_5XX, sent once, the run goes on", func(t *testing.T) {
		t.Parallel()
		srv, c, r := signedIn(t, nil)
		srv.Handle(hrvPath, status(503))
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "HTTP_5XX" || res.Status != 503 || res.Data != nil {
			t.Fatalf("result = %+v", res)
		}
		if n := len(srv.Calls(hrvPath)); n != 1 || len(r.waits) != 0 || c.Stopped() != nil {
			t.Fatalf("%d calls, waits %v, stopped %v", n, r.waits, c.Stopped())
		}
	})
	t.Run("EP-437 tokens with no login: AUTH, the run stops as a dead session (assumption S2), nothing sent", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		c, _ := newClient(srv, &gconnect.Tokens{})
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "AUTH" || c.Stopped() == nil || c.Stopped().Reason != gconnect.StopSessionDead {
			t.Fatalf("result %+v, stopped %v", res, c.Stopped())
		}
		if n := len(srv.Calls("")); n != 0 {
			t.Fatalf("%d requests", n)
		}
	})

	for _, n := range []int{4, 5} {
		id := map[int]string{4: "BVA-431 four 403 answers in a row: the run goes on", 5: "BVA-432 the fifth 403 in a row: StopBlocked, tokens kept"}[n]
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			srv, c, r := signedIn(t, nil)
			srv.Set(func(k *gconnecttest.Control) { k.DataStatus["/hrv-service/"] = 403 })
			for range n {
				c.Get(ctx, ep("hrv"), hrv)
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
	t.Run("BVA-436 4 answers 403, one success, 4 more 403: the streak restarted, the run goes on", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) { k.DataStatus["/hrv-service/"] = 403 })
		for i := range 9 {
			if i == 4 {
				c.Get(ctx, ep("social_profile"), gconnect.Params{})
				continue
			}
			c.Get(ctx, ep("hrv"), hrv)
		}
		if c.Stopped() != nil {
			t.Fatalf("stopped = %v", c.Stopped())
		}
	})
	for _, c := range []struct {
		id   string
		code int
		want string
	}{
		{"BVA-437 HTTP 499, the last 4xx: HTTP_4XX", 499, "HTTP_4XX"},
		{"BVA-438 HTTP 500, the first 5xx: HTTP_5XX", 500, "HTTP_5XX"},
	} {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			srv, cl, _ := signedIn(t, nil)
			srv.Handle(hrvPath, status(c.code))
			if res := cl.Get(ctx, ep("hrv"), hrv); res.Code != c.want || res.Status != c.code {
				t.Fatalf("result = %+v", res)
			}
		})
	}

	t.Run("BVA-433 one 429 then success: the data, after waiting Retry-After once", func(t *testing.T) {
		t.Parallel()
		srv, c, r := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) { k.FailOnce[social] = 429 })
		if res := c.Get(ctx, ep("social_profile"), gconnect.Params{}); res.Code != "" {
			t.Fatalf("result = %+v", res)
		}
		if !slices.Equal(r.waits, []time.Duration{time.Second}) {
			t.Fatalf("waits = %v", r.waits)
		}
	})
	t.Run("BVA-434 a third 429, over the 2 retry budget: RATE_LIMIT, StopRateLimited", func(t *testing.T) {
		t.Parallel()
		srv, c, r := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) { k.DataStatus[social] = 429 })
		res := c.Get(ctx, ep("social_profile"), gconnect.Params{})
		if res.Code != "RATE_LIMIT" || c.Stopped() == nil || c.Stopped().Reason != gconnect.StopRateLimited {
			t.Fatalf("result %+v, stopped %v", res, c.Stopped())
		}
		if !slices.Equal(r.waits, []time.Duration{time.Second, time.Second}) || len(srv.Calls(social)) != 3 {
			t.Fatalf("waits %v, calls %d", r.waits, len(srv.Calls(social)))
		}
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "RATE_LIMIT" || len(srv.Calls(hrvPath)) != 0 {
			t.Fatalf("after the stop: %+v", res)
		}
	})
	t.Run("BVA-435 two 429 then success: the data, after waiting Retry-After twice", func(t *testing.T) {
		t.Parallel()
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
		res := c.Get(ctx, ep("hrv"), hrv)
		if res.Code != "" || string(res.Data) != `{"ok":1}` || !slices.Equal(r.waits, []time.Duration{3 * time.Second, 3 * time.Second}) {
			t.Fatalf("result %+v, waits %v", res, r.waits)
		}
	})
	t.Run("BVA-442 429 without Retry-After: waits the backoff, 5 s then 10 s, then stops", func(t *testing.T) {
		t.Parallel()
		srv, c, r := signedIn(t, nil)
		srv.Handle(hrvPath, status(429))
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "RATE_LIMIT" || !slices.Equal(r.waits, []time.Duration{5 * time.Second, 10 * time.Second}) {
			t.Fatalf("result %+v, waits %v", res, r.waits)
		}
	})

	t.Run("EG-432 HTTP 403 on the profile: StopBlocked at once, no daily request", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) { k.DataStatus[social] = 403 })
		if _, err := c.Profile(ctx); err == nil {
			t.Fatal("Profile succeeded")
		}
		if c.Stopped() == nil || c.Stopped().Reason != gconnect.StopBlocked || !strings.HasPrefix(c.Stopped().Msg, "Garmin refused the profile request (HTTP 403).") {
			t.Fatalf("stopped = %v", c.Stopped())
		}
		c.Safe(ctx, ep("hrv"), hrv)
		if n := len(srv.Calls(hrvPath)); n != 0 {
			t.Fatalf("%d hrv calls after the stop", n)
		}
	})
	t.Run("CL-462 a 403 from GetURL on the profile path: FORBIDDEN, the run goes on", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) { k.FailOnce[social] = 403 })
		if res := c.GetURL(ctx, srv.URL()+social, nil); res.Code != "FORBIDDEN" || c.Stopped() != nil {
			t.Fatalf("result %+v, stopped %v", res, c.Stopped())
		}
	})
	t.Run("CL-464 a new client: Stopped is nil", func(t *testing.T) {
		t.Parallel()
		if s := (&gconnect.Client{}).Stopped(); s != nil {
			t.Fatalf("stopped = %v", s)
		}
	})
}

func TestRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		id, raw string
		want    time.Duration
		ok      bool
	}{
		{"EG-431 an HTTP date 5 s ahead: wait 5 s", now.Add(5 * time.Second).Format(http.TimeFormat), 5 * time.Second, true},
		{"EG-443 120 seconds: capped at 60 s", "120", time.Minute, true},
		{"EG-444 no header: no Retry-After", "", 0, false},
		{"EG-445 1.5 seconds: fractional seconds count", "1.5", 1500 * time.Millisecond, true},
		{"EG-446 an HTTP date in the past: wait 0", now.Add(-time.Hour).Format(http.TimeFormat), 0, true},
		{"EG-447 Infinity: ignored", "Infinity", 0, false},
		{"EG-449 a word that is neither seconds nor a date: ignored", "soon", 0, false},
		{"BVA-439 59 seconds, under the cap: wait 59 s", "59", 59 * time.Second, true},
		{"BVA-440 60 seconds, the cap: wait 60 s", "60", time.Minute, true},
		{"BVA-441 61 seconds, over the cap: wait 60 s", "61", time.Minute, true},
		{"BVA-444 an HTTP date 61 s ahead, over the cap: wait 60 s", now.Add(61 * time.Second).Format(http.TimeFormat), time.Minute, true},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			got, ok := gconnect.RetryAfter(c.raw, now)
			if got != c.want || ok != c.ok {
				t.Fatalf("RetryAfter(%q) = %v %v", c.raw, got, ok)
			}
		})
	}
}

func TestClientHeaders(t *testing.T) {
	t.Parallel()
	t.Run("EG-433 DI requests: every native header; legacy requests: the mobile User-Agent", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		c.Get(ctx, ep("hrv"), hrv)
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
		c.Get(ctx, ep("hrv"), hrv)
		if ua := srv.Calls(hrvPath)[0].Header.Get("User-Agent"); ua != "com.garmin.android.apps.connectmobile" {
			t.Errorf("legacy User-Agent = %q", ua)
		}
	})
	t.Run("EG-434 goals: start=1 and the Sec-Fetch-Site: same-origin header", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		c.Get(ctx, ep("goals"), gconnect.Params{})
		calls := srv.Calls("/goal-service/goal/goals")
		if len(calls) != 1 || !strings.Contains(calls[0].Query, "start=1&") || calls[0].Header.Get("Sec-Fetch-Site") != "same-origin" {
			t.Fatalf("calls = %+v", calls)
		}
	})
	t.Run("CL-458 GetURL extra headers in any case: they replace the defaults", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		c.GetURL(ctx, srv.URL()+hrvPath, http.Header{"user-agent": {"mine"}})
		if ua := srv.Calls(hrvPath)[0].Header.Values("User-Agent"); !slices.Equal(ua, []string{"mine"}) {
			t.Fatalf("User-Agent = %q", ua)
		}
	})
}

func TestClientErrors(t *testing.T) {
	t.Parallel()
	t.Run("EG-435 a 500 body with secrets: redacted in the [skip] line and cut to 200 characters", func(t *testing.T) {
		t.Parallel()
		srv, c, r := signedIn(t, nil)
		srv.Handle(hrvPath, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"Bearer abc.def.ghi","password":"x"}` + strings.Repeat("é", 300)))
		})
		if c.Safe(ctx, ep("hrv"), hrv) != nil {
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
	t.Run("EG-436 Gap 0: 500 ms between requests; a negative Gap: none", func(t *testing.T) {
		// Not parallel: the wait it reads depends on how fast the first request returns.
		_, c, r := signedIn(t, nil)
		c.Gap = 0
		c.Get(ctx, ep("social_profile"), gconnect.Params{})
		c.Get(ctx, ep("social_profile"), gconnect.Params{})
		if len(r.waits) != 1 || r.waits[0] <= 0 || r.waits[0] > 500*time.Millisecond {
			t.Fatalf("waits = %v", r.waits)
		}
	})

	backoff := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 60 * time.Second}
	t.Run("EG-437 a timeout on every attempt: retried 5 times, backoff min(5000*2^n + jitter, 60000) ms, then TIMEOUT", func(t *testing.T) {
		t.Parallel()
		srv, c, r := signedIn(t, nil)
		srv.Handle(hrvPath, func(_ http.ResponseWriter, req *http.Request) { <-req.Context().Done() })
		c.Timeout = 100 * time.Millisecond
		res := c.Get(ctx, ep("hrv"), hrv)
		if res.Code != "TIMEOUT" || res.Err != "Timeout after 100ms" || !slices.Equal(r.waits, backoff) || len(srv.Calls(hrvPath)) == 0 {
			t.Fatalf("result %+v, waits %v, calls %d", res, r.waits, len(srv.Calls(hrvPath)))
		}
		if !r.has("dim", "[client] Timeout after 100ms on hrv on attempt 1, retrying in 5000ms", "") {
			t.Fatalf("logs = %v", r.logs)
		}
	})
	t.Run("EG-438 a network error on every attempt: retried 5 times, jitter added, the 60 s cap holds, NETWORK", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		c, r := newClient(srv, &gconnect.Tokens{DI: &gconnect.DI{AccessToken: "a", RefreshToken: "refresh-1", ExpiresAt: 1 << 40}})
		c.Auth.Hosts = gconnect.SameHost(deadURL(t))
		c.Jitter = func() time.Duration { return 999 * time.Millisecond }
		res := c.Get(ctx, ep("hrv"), hrv)
		want := []time.Duration{5999 * time.Millisecond, 10999 * time.Millisecond, 20999 * time.Millisecond, 40999 * time.Millisecond, 60 * time.Second}
		if res.Code != "NETWORK" || !strings.HasPrefix(res.Err, "Network error: ") || !slices.Equal(r.waits, want) {
			t.Fatalf("result %+v, waits %v", res, r.waits)
		}
		if c.Stopped() != nil {
			t.Fatal("a network error stopped the run")
		}
	})
	t.Run("BVA-443 Jitter nil: each backoff is the base plus 0 to 999 ms, capped at 60 s", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		c, r := newClient(srv, &gconnect.Tokens{DI: &gconnect.DI{AccessToken: "a", RefreshToken: "refresh-1", ExpiresAt: 1 << 40}})
		c.Auth.Hosts = gconnect.SameHost(deadURL(t))
		c.Jitter = nil
		c.Get(ctx, ep("hrv"), hrv)
		if len(r.waits) != 5 {
			t.Fatalf("waits = %v", r.waits)
		}
		for i, w := range r.waits {
			lo := min(backoff[i], 60*time.Second)
			if w < lo || w > min(lo+999*time.Millisecond, 60*time.Second) {
				t.Errorf("wait %d = %v, want %v + [0, 999] ms, at most 60 s", i, w, lo)
			}
		}
	})
	t.Run("EG-442 a cancelled context: NETWORK at once, no retries", func(t *testing.T) {
		t.Parallel()
		_, c, r := signedIn(t, nil)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		res := c.Get(cctx, ep("hrv"), hrv)
		if res.Code != "NETWORK" || len(r.waits) != 0 {
			t.Fatalf("result %+v, waits %v", res, r.waits)
		}
	})
	t.Run("EG-450 Sleep nil and the context ends during the 5 s backoff: NETWORK long before 5 s", func(t *testing.T) {
		t.Parallel()
		c := &gconnect.Client{Auth: &gconnect.Auth{Hosts: gconnect.SameHost(deadURL(t))}, Gap: -1,
			Tokens: &gconnect.Tokens{DI: &gconnect.DI{AccessToken: "a", RefreshToken: "refresh-1", ExpiresAt: 1 << 40}}}
		cctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		start := time.Now()
		res := c.Get(cctx, ep("hrv"), hrv)
		if res.Code != "NETWORK" || time.Since(start) > 3*time.Second {
			t.Fatalf("result %+v after %v", res, time.Since(start))
		}
	})
	t.Run("EG-451 Sleep nil and Gap 30 ms: the second request starts at least 30 ms after the first", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		c.Sleep, c.Gap = nil, 30*time.Millisecond
		start := time.Now()
		c.Get(ctx, ep("social_profile"), gconnect.Params{})
		c.Get(ctx, ep("social_profile"), gconnect.Params{})
		if el := time.Since(start); el < 30*time.Millisecond || len(srv.Calls(social)) != 2 {
			t.Fatalf("two requests in %v", el)
		}
	})
	t.Run("EG-452 the context ends during the Gap wait: NETWORK, the request is not sent", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		c.Sleep, c.Gap = nil, time.Hour
		c.Get(ctx, ep("social_profile"), gconnect.Params{})
		cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		if res := c.Get(cctx, ep("social_profile"), gconnect.Params{}); res.Code != "NETWORK" || len(srv.Calls(social)) != 1 {
			t.Fatalf("result %+v, calls %d", res, len(srv.Calls(social)))
		}
	})
	t.Run("EG-453 Sleep fails during a 429 wait or a backoff: NETWORK, no further attempt", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		c.Sleep = func(context.Context, time.Duration) error { return errors.New("interrupted") }
		srv.Handle(hrvPath, status(429))
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "NETWORK" || len(srv.Calls(hrvPath)) != 1 {
			t.Fatalf("429 wait: %+v, calls %d", res, len(srv.Calls(hrvPath)))
		}
		c.Auth.Hosts = gconnect.SameHost(deadURL(t))
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "NETWORK" || !strings.Contains(res.Err, "interrupted") {
			t.Fatalf("backoff: %+v", res)
		}
	})
	t.Run("EG-454 an access_token in a URL that fails: never in Result.Err", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		c, _ := newClient(srv, &gconnect.Tokens{DI: &gconnect.DI{AccessToken: "a", RefreshToken: "refresh-1", ExpiresAt: 1 << 40}})
		res := c.GetURL(ctx, deadURL(t)+"/x?access_token=SECRET123", nil)
		if res.Code != "NETWORK" || strings.Contains(res.Err, "SECRET123") {
			t.Fatalf("result %+v", res)
		}
	})
	t.Run("EG-458 a malformed URL in GetURL: NETWORK after the retries, nothing sent", func(t *testing.T) {
		t.Parallel()
		srv, c, r := signedIn(t, nil)
		if res := c.GetURL(ctx, "http://bad host/x", nil); res.Code != "NETWORK" || len(r.waits) != 5 || len(srv.Calls("")) != 0 {
			t.Fatalf("result %+v, waits %v", res, r.waits)
		}
	})
	t.Run("EG-459 a body cut short by the server: NETWORK after the retries, no data", func(t *testing.T) {
		t.Parallel()
		srv, c, r := signedIn(t, nil)
		srv.Handle(hrvPath, func(w http.ResponseWriter, _ *http.Request) {
			conn, buf, _ := w.(http.Hijacker).Hijack()
			buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"a\":")
			buf.Flush()
			conn.Close()
		})
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "NETWORK" || res.Data != nil || len(r.waits) != 5 {
			t.Fatalf("result %+v, waits %v", res, r.waits)
		}
	})
	t.Run("EG-455 Log nil: a failed Safe returns nil and does not panic", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		c.Log = nil
		srv.Handle(hrvPath, status(500))
		if c.Safe(ctx, ep("hrv"), hrv) != nil {
			t.Fatal("data on a 500")
		}
	})
	t.Run("EG-456 Stopped read from another goroutine while requests run: no data race", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) { k.DataStatus["/hrv-service/"] = 403 })
		done := make(chan struct{})
		go func() {
			defer close(done)
			for c.Stopped() == nil {
				time.Sleep(time.Millisecond)
			}
		}()
		for range 5 {
			c.Get(ctx, ep("hrv"), hrv)
		}
		<-done
	})
	t.Run("CL-437 a 200 that is not JSON: BAD_JSON; a 404: HTTP_4XX; neither stops the run", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		srv.Handle(hrvPath, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<html>")) })
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "BAD_JSON" || res.Status != 200 {
			t.Fatalf("result = %+v", res)
		}
		if res := c.Get(ctx, ep("stress"), hrv); res.Code != "HTTP_4XX" || res.Status != 404 {
			t.Fatalf("result = %+v", res)
		}
		if c.Stopped() != nil {
			t.Fatal("stopped")
		}
	})
	t.Run("BVA-445 an answer of 1 MB + 1 byte: TOO_LARGE; exactly 1 MB: read in full", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		big := func(n int) []byte { return []byte(`"` + strings.Repeat("a", n-2) + `"`) }
		srv.Handle(hrvPath, func(w http.ResponseWriter, _ *http.Request) { w.Write(big(1<<20 + 1)) })
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "TOO_LARGE" || res.Data != nil || !strings.Contains(res.Err, "over 1 MB") {
			t.Fatalf("result = %+v", res.Err)
		}
		srv.Handle(hrvPath, func(w http.ResponseWriter, _ *http.Request) { w.Write(big(1 << 20)) })
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "" || len(res.Data) != 1<<20 {
			t.Fatalf("1 MB answer: %q, %d bytes", res.Code, len(res.Data))
		}
	})
	t.Run("CL-438 Profile: the display name", func(t *testing.T) {
		t.Parallel()
		_, c, _ := signedIn(t, nil)
		if dn, err := c.Profile(ctx); err != nil || dn != "displayName-redacted" {
			t.Fatalf("Profile = %q %v", dn, err)
		}
	})
	t.Run("CL-460 Profile answers without displayName: an error, no name", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		srv.Handle(social, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"id":1}`)) })
		if dn, err := c.Profile(ctx); err == nil || dn != "" {
			t.Fatalf("Profile = %q %v", dn, err)
		}
	})
	t.Run("CL-461 Profile answers 500: an error that names HTTP 500", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		srv.Handle(social, status(500))
		if dn, err := c.Profile(ctx); err == nil || dn != "" || !strings.Contains(err.Error(), "HTTP 500") {
			t.Fatalf("Profile = %q %v", dn, err)
		}
	})
	t.Run("CL-459 Safe on success: the data, no log line", func(t *testing.T) {
		t.Parallel()
		srv, c, r := signedIn(t, nil)
		srv.Handle(hrvPath, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"hrv":1}`)) })
		if data := c.Safe(ctx, ep("hrv"), hrv); string(data) != `{"hrv":1}` || len(r.logs) != 0 {
			t.Fatalf("data %s, logs %v", data, r.logs)
		}
	})
}

// expired is a DI login whose access token expired, so the next request refreshes it first.
func expired() *gconnect.Tokens {
	return &gconnect.Tokens{DI: &gconnect.DI{AccessToken: "old", RefreshToken: "refresh-1", ClientID: "GARMIN_CONNECT_MOBILE_ANDROID_DI_2025Q2", ExpiresAt: 1}}
}

func TestClientRefresh(t *testing.T) {
	t.Parallel()
	t.Run("EG-439 a 5xx on the refresh before a request: StopRefreshFailed, tokens kept, no data request", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		srv.Set(func(k *gconnecttest.Control) { k.RefreshFail = 500 })
		tok := expired()
		c, r := newClient(srv, tok)
		res := c.Get(ctx, ep("hrv"), hrv)
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
	t.Run("EG-440 a 5xx on the refresh after a 401: StopRefreshFailed, tokens kept", func(t *testing.T) {
		t.Parallel()
		srv, c, r := signedIn(t, nil)
		before := c.Tokens
		srv.Set(func(k *gconnecttest.Control) {
			k.DataStatus["/hrv-service/"] = 401
			k.RefreshFail = 503
		})
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "AUTH" {
			t.Fatalf("result = %+v", res)
		}
		if c.Stopped() == nil || c.Stopped().Reason != gconnect.StopRefreshFailed || c.Tokens != before || len(r.saved) != 0 {
			t.Fatalf("stopped %v, saved %d", c.Stopped(), len(r.saved))
		}
	})
	t.Run("EG-441 a 400 on the refresh before a request: StopSessionDead", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		srv.Set(func(k *gconnecttest.Control) { k.RefreshFail = 400 })
		c, _ := newClient(srv, expired())
		c.Get(ctx, ep("hrv"), hrv)
		if c.Stopped() == nil || c.Stopped().Reason != gconnect.StopSessionDead {
			t.Fatalf("stopped = %v", c.Stopped())
		}
	})
	t.Run("EG-457 a 400 on the refresh after a 401: StopSessionDead", func(t *testing.T) {
		t.Parallel()
		srv, c, _ := signedIn(t, nil)
		srv.Set(func(k *gconnecttest.Control) {
			k.DataStatus["/hrv-service/"] = 401
			k.RefreshFail = 400
		})
		if res := c.Get(ctx, ep("hrv"), hrv); res.Code != "AUTH" || c.Stopped() == nil || c.Stopped().Reason != gconnect.StopSessionDead {
			t.Fatalf("result %+v, stopped %v", res, c.Stopped())
		}
	})
	t.Run("CL-435 an expired token: refreshed before the request, OnTokens gets the new tokens", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		c, r := newClient(srv, expired())
		if res := c.Get(ctx, ep("social_profile"), gconnect.Params{}); res.Code != "" {
			t.Fatalf("result = %+v", res)
		}
		if len(r.saved) != 1 || r.saved[0].DI.AccessToken == "old" || c.Tokens != r.saved[0] {
			t.Fatal("the refreshed tokens did not reach OnTokens")
		}
	})
	t.Run("CL-436 OnTokens fails: a SAVE_FAILED warning, the run goes on with the new tokens", func(t *testing.T) {
		t.Parallel()
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
	t.Run("CL-463 OnTokens nil: a refresh still replaces Client.Tokens", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		c, _ := newClient(srv, expired())
		c.OnTokens = nil
		if res := c.Get(ctx, ep("social_profile"), gconnect.Params{}); res.Code != "" || c.Tokens.DI.AccessToken == "old" {
			t.Fatalf("result %+v, tokens %+v", res, c.Tokens.DI)
		}
	})
}
