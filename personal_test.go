package gconnect_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/danihrndzld/gconnect"
	"github.com/danihrndzld/gconnect/gconnecttest"
)

// The Personal API behavior below comes from Garmin's documentation
// (developer.garmin.com/gc-developer-program/personal/personal-faq), Garmin's own Python
// client (github.com/garmin/connect-personal-api-python) and probes of the real service on
// 2026-10-24: a wrong key answers 401 with no body, a missing key 400, and a good key the bare
// JWT, which lives 24 hours.

const apiKey = "right-api-key"

func personalTokens(t *testing.T, srv *gconnecttest.Server) *gconnect.Tokens {
	t.Helper()
	tok, err := (&gconnect.Auth{Hosts: srv.Hosts()}).ExchangeAPIKey(context.Background(), apiKey)
	if err != nil {
		t.Fatal(err)
	}
	srv.Reset()
	return tok
}

func TestExchangeAPIKey(t *testing.T) {
	t.Parallel()
	t.Run("EP-493 the accepted key: a Personal login that keeps the key and expires at the JWT exp", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		sut := &gconnect.Auth{Hosts: srv.Hosts()}
		before := time.Now().Unix()

		tok, err := sut.ExchangeAPIKey(context.Background(), apiKey)

		if err != nil || tok.Kind() != gconnect.KindPersonal || tok.Personal.APIKey != apiKey || tok.Personal.AccessToken == "" {
			t.Fatalf("got %+v, %v", tok, err)
		}
		if exp := tok.AccessExpiresAt(); exp < before+3600 || exp > before+3601 { // the fake's TokenTTL is 3600 s
			t.Fatalf("expires at %d, want now + 3600", exp)
		}
	})
	t.Run("BVA-448 a token without an exp claim: it expires 24 hours after now", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		hosts := front(t, srv, map[string]http.HandlerFunc{"POST " + gconnect.PersonalTokenPath: reply(200, "eyJhbGciOiJub25lIn0.e30.sig")})
		sut := &gconnect.Auth{Hosts: hosts, Now: func() time.Time { return time.Unix(1_000_000, 0) }}

		tok, err := sut.ExchangeAPIKey(context.Background(), apiKey)

		if err != nil || tok.Personal.ExpiresAt != 1_000_000+86_400 {
			t.Fatalf("got %+v, %v", tok, err)
		}
	})
	t.Run("EP-494 the token sent as a JSON string: read as the bare token", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		hosts := front(t, srv, map[string]http.HandlerFunc{"POST " + gconnect.PersonalTokenPath: reply(200, `"eyJhbGciOiJub25lIn0.e30.sig"`)})

		tok, err := (&gconnect.Auth{Hosts: hosts}).ExchangeAPIKey(context.Background(), apiKey)

		if err != nil || tok.Personal.AccessToken != "eyJhbGciOiJub25lIn0.e30.sig" {
			t.Fatalf("got %+v, %v", tok, err)
		}
	})
	t.Run("EP-495 an empty key: ErrSessionDead and nothing sent", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)

		_, err := (&gconnect.Auth{Hosts: srv.Hosts()}).ExchangeAPIKey(context.Background(), "")

		if !errors.Is(err, gconnect.ErrSessionDead) || len(srv.Calls("")) != 0 {
			t.Fatalf("err %v, %d requests", err, len(srv.Calls("")))
		}
	})
	// A dead key logs the user out of API key mode, so only Garmin's refusals may say dead.
	for _, c := range []struct {
		name   string
		status int
		dead   bool
	}{
		{"DT-R484 a wrong, expired or deleted key (401): the key is dead", 401, true},
		{"DT-R485 a request without a key (400): the key is dead", 400, true},
		{"DT-R486 rate limited (429): a plain error, the key is kept", 429, false},
		{"DT-R487 a server error (503): a plain error, the key is kept", 503, false},
		{"DT-R488 refused by a firewall (403): a plain error, the key is kept", 403, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			srv := fakeGarmin(t)
			srv.Set(func(ct *gconnecttest.Control) { ct.APIKeyStatus = c.status })

			_, err := (&gconnect.Auth{Hosts: srv.Hosts()}).ExchangeAPIKey(context.Background(), apiKey)

			if err == nil || errors.Is(err, gconnect.ErrSessionDead) != c.dead {
				t.Fatalf("err %v, want dead=%v", err, c.dead)
			}
		})
	}
	t.Run("DT-R489 a wrong key on the fake: 401, so the key is dead", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)

		_, err := (&gconnect.Auth{Hosts: srv.Hosts()}).ExchangeAPIKey(context.Background(), "wrong-key")

		if !errors.Is(err, gconnect.ErrSessionDead) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("EG-483 the network fails: a plain error, the key is kept", func(t *testing.T) {
		t.Parallel()
		sut := &gconnect.Auth{Hosts: gconnect.SameHost(deadURL(t))}

		_, err := sut.ExchangeAPIKey(context.Background(), apiKey)

		if err == nil || errors.Is(err, gconnect.ErrSessionDead) {
			t.Fatalf("err %v", err)
		}
	})
	for _, c := range []struct{ name, body string }{
		{"EG-484 a 200 with an HTML page: an error that is not dead", "<html>Just a moment...</html>"},
		{"EG-485 a 200 with a JSON object: an error that is not dead", `{"error":"x"}`},
		{"EG-486 a 200 with an empty body: an error that is not dead", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			srv := fakeGarmin(t)
			hosts := front(t, srv, map[string]http.HandlerFunc{"POST " + gconnect.PersonalTokenPath: reply(200, c.body)})

			_, err := (&gconnect.Auth{Hosts: hosts}).ExchangeAPIKey(context.Background(), apiKey)

			if err == nil || errors.Is(err, gconnect.ErrSessionDead) {
				t.Fatalf("err %v", err)
			}
		})
	}
}

func TestPersonalTokens(t *testing.T) {
	t.Parallel()
	t.Run("EP-496 Kind: a key alone is Personal, a key next to a DI login wins, an empty key is none", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct {
			body string
			want gconnect.Kind
		}{
			{`{"personal":{"api_key":"k"}}`, gconnect.KindPersonal},
			{`{"personal":{"api_key":"k"},"di":{"access_token":"a","client_id":"c"}}`, gconnect.KindPersonal},
			{`{"personal":{"api_key":""}}`, gconnect.KindNone},
		} {
			if got := parse(t, c.body).Kind(); got != c.want {
				t.Errorf("%s: kind %d, want %d", c.body, got, c.want)
			}
		}
	})
	t.Run("CL-501 Personal headers: the Bearer access token and no native app headers", func(t *testing.T) {
		t.Parallel()
		tok := parse(t, `{"personal":{"api_key":"k","access_token":"acc","expires_at":1}}`)

		h := gconnect.Headers(tok)

		if h.Get("Authorization") != "Bearer acc" || h.Get("Accept") != "application/json" || len(h) != 2 {
			t.Fatalf("headers %v", h)
		}
	})
	t.Run("ST-T431 a Personal access token near expiry: Fresh buys a new one with the key, once", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		tok := personalTokens(t, srv)
		sut := &gconnect.Auth{Hosts: srv.Hosts(), Now: func() time.Time { return time.Unix(tok.AccessExpiresAt()-60, 0) }}

		next, refreshed, err := sut.Fresh(context.Background(), tok, 15*time.Minute)

		if err != nil || !refreshed || next.Personal.APIKey != apiKey || next.Personal.AccessToken == tok.Personal.AccessToken {
			t.Fatalf("got %+v %v %v", next, refreshed, err)
		}
		if n := len(srv.Calls(gconnect.PersonalTokenPath)); n != 1 {
			t.Fatalf("%d token requests, want 1", n)
		}
	})
}

func TestPersonalClient(t *testing.T) {
	t.Parallel()
	t.Run("AC-401 a Personal login reads from the Personal API host, never from connectapi", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		srv.Handle("/hrv-service/hrv/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"hrvSummary":{}}`)) })
		tok := personalTokens(t, srv)
		sut, _ := newClient(srv, tok)
		hosts := srv.Hosts()
		hosts.ConnectAPI = deadURL(t)
		sut.Auth.Hosts = hosts

		r := sut.Get(context.Background(), ep("hrv"), gconnect.Params{Date: "2026-10-07"})

		if r.Code != "" || string(r.Data) != `{"hrvSummary":{}}` {
			t.Fatalf("result %+v", r)
		}
	})
	t.Run("ST-T432 the access token expired: the client buys a new one, saves it, and gets the data", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		srv.Handle("/hrv-service/hrv/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{}`)) })
		tok := personalTokens(t, srv)
		tok.Personal.ExpiresAt = 1
		sut, rec := newClient(srv, tok)

		r := sut.Get(context.Background(), ep("hrv"), gconnect.Params{Date: "2026-10-07"})

		if r.Code != "" || len(rec.saved) != 1 || rec.saved[0].Personal.APIKey != apiKey {
			t.Fatalf("result %+v, saved %d", r, len(rec.saved))
		}
	})
	t.Run("ST-T433 Garmin refuses the key when the token is renewed: the run stops as a dead session", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		tok := personalTokens(t, srv)
		tok.Personal.ExpiresAt = 1
		srv.Set(func(c *gconnecttest.Control) { c.APIKeyStatus = 401 })
		sut, _ := newClient(srv, tok)

		sut.Get(context.Background(), ep("hrv"), gconnect.Params{Date: "2026-10-07"})

		if s := sut.Stopped(); s == nil || s.Reason != gconnect.StopSessionDead {
			t.Fatalf("stopped %+v", s)
		}
	})
	t.Run("EG-487 a path the Personal API does not serve: HTTP 404, and the run goes on", func(t *testing.T) {
		t.Parallel()
		srv := fakeGarmin(t)
		sut, _ := newClient(srv, personalTokens(t, srv))

		r := sut.Get(context.Background(), ep("training_readiness"), gconnect.Params{Date: "2026-10-07"})

		if r.Status != 404 || r.Code != "HTTP_4XX" || sut.Stopped() != nil {
			t.Fatalf("result %+v, stopped %v", r, sut.Stopped())
		}
	})
}

func TestPersonalEndpoints(t *testing.T) {
	t.Parallel()
	t.Run("CL-502 PersonalRanges: the 5 paths of Garmin's Python client, type personal", func(t *testing.T) {
		t.Parallel()
		p := gconnect.Params{Start: "2026-10-01", End: "2026-10-07"}
		want := map[string]string{
			"personal_daily_summaries": "http://x/usersummary-service/v2/dailysummaries?startDate=2026-10-01&endDate=2026-10-07",
			"personal_sleep":           "http://x/sleep-service/sleep/dailySleepSummariesByDate?startDate=2026-10-01&endDate=2026-10-07",
			"personal_hrv":             "http://x/hrv-service/hrv/daily/2026-10-01/2026-10-07",
			"personal_body_battery":    "http://x/wellness-service/wellness/bodyBattery/reports/daily?startDate=2026-10-01&endDate=2026-10-07",
			"personal_fitness_age":     "http://x/fitnessage-service/fitnessage/v2/2026-10-07",
		}

		got := gconnect.PersonalRanges()

		if len(got) != len(want) {
			t.Fatalf("%d endpoints, want %d", len(got), len(want))
		}
		for _, e := range got {
			if e.Type != "personal" || e.URL("http://x", p) != want[e.Name] {
				t.Errorf("%s (%s): %s", e.Name, e.Type, e.URL("http://x", p))
			}
		}
	})
	t.Run("CL-503 Redact: an ApiKey header value is hidden", func(t *testing.T) {
		t.Parallel()

		got := gconnect.Redact("Authorization: ApiKey s3cr3t-key and ApiKey other-key")

		if strings.Contains(got, "s3cr3t-key") || strings.Contains(got, "other-key") {
			t.Fatalf("got %q", got)
		}
	})
}
