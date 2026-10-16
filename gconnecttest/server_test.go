package gconnecttest_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/danihrndzld/gconnect"
	"github.com/danihrndzld/gconnect/gconnecttest"
)

// signedIn starts a fake and returns it with an access token it issued.
func signedIn(t *testing.T) (*gconnecttest.Server, string) {
	t.Helper()
	srv := gconnecttest.New()
	t.Cleanup(srv.Close)
	tok, err := (&gconnect.Auth{Hosts: srv.Hosts()}).Login(context.Background(), "e@example.invalid", "right-password")
	if err != nil {
		t.Fatal(err)
	}
	return srv, tok.DI.AccessToken
}

// get sends a GET to path with the access token and returns the status and body.
func get(t *testing.T, srv *gconnecttest.Server, path, access string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL()+path, nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestControl(t *testing.T) {
	t.Parallel()
	t.Run("CL-481 a new fake: the documented default knobs", func(t *testing.T) {
		t.Parallel()
		srv := gconnecttest.New()
		defer srv.Close()
		c := srv.Control()
		if c.Password != "right-password" || c.MobileLogin != "ok" || c.DIAuth != "ok" || c.TokenTTL != 3600 ||
			c.RefreshFail != 0 || c.Widget429 || c.WidgetStatus != 0 || len(c.DataStatus) != 0 || len(c.FailOnce) != 0 {
			t.Fatalf("defaults = %+v", c)
		}
	})
	t.Run("CL-482 Control returns a copy: changing it, maps included, does not change the fake", func(t *testing.T) {
		t.Parallel()
		srv, access := signedIn(t)
		c := srv.Control()
		c.Password = "changed"
		c.DataStatus["/hrv-service/"] = 500
		c.FailOnce["/hrv-service/"] = 500
		if got := srv.Control(); got.Password != "right-password" || len(got.DataStatus) != 0 || len(got.FailOnce) != 0 {
			t.Fatalf("the fake changed: %+v", got)
		}
		if status, _ := get(t, srv, "/hrv-service/hrv/2026-10-07", access); status != 404 {
			t.Fatalf("status %d, want 404 (no route, no forced status)", status)
		}
	})
	t.Run("CL-483 Reset after changes: default knobs, no calls, issued tokens still valid", func(t *testing.T) {
		t.Parallel()
		srv, access := signedIn(t)
		srv.Set(func(c *gconnecttest.Control) { c.DataStatus["/"] = 500; c.Password = "x" })
		srv.Reset()
		if n := len(srv.Calls("")); n != 0 {
			t.Fatalf("%d calls after Reset", n)
		}
		if c := srv.Control(); c.Password != "right-password" || len(c.DataStatus) != 0 {
			t.Fatalf("knobs after Reset: %+v", c)
		}
		if status, _ := get(t, srv, "/userprofile-service/socialProfile", access); status != 200 {
			t.Fatalf("an issued token answered %d after Reset", status)
		}
	})
}

func TestPrefixMatching(t *testing.T) {
	t.Parallel()
	// Go iterates maps in a random order, so each case runs 30 times: a first match would fail.
	const runs = 30
	t.Run("EP-481 DataStatus: the longest matching prefix wins, every time", func(t *testing.T) {
		t.Parallel()
		srv, access := signedIn(t)
		srv.Set(func(c *gconnecttest.Control) {
			c.DataStatus["/wellness-service/"] = 500
			c.DataStatus["/wellness-service/wellness/dailyStress/"] = 418
			c.DataStatus["/w"] = 502
		})
		for range runs {
			if s, _ := get(t, srv, "/wellness-service/wellness/dailyStress/2026-10-07", access); s != 418 {
				t.Fatalf("dailyStress answered %d, want 418", s)
			}
			if s, _ := get(t, srv, "/wellness-service/wellness/daily/spo2/2026-10-07", access); s != 500 {
				t.Fatalf("spo2 answered %d, want 500", s)
			}
			if s, _ := get(t, srv, "/weight-service/x", access); s != 502 {
				t.Fatalf("weight answered %d, want 502", s)
			}
		}
	})
	t.Run("EP-482 FailOnce: the longest matching prefix answers once and only that entry is used up", func(t *testing.T) {
		t.Parallel()
		for range runs {
			srv, access := signedIn(t)
			srv.Set(func(c *gconnecttest.Control) {
				c.FailOnce["/hrv-service/"] = 500
				c.FailOnce["/hrv-service/hrv/"] = 429
			})
			if s, _ := get(t, srv, "/hrv-service/hrv/2026-10-07", access); s != 429 {
				t.Fatalf("first answer %d, want 429", s)
			}
			if fo := srv.Control().FailOnce; len(fo) != 1 || fo["/hrv-service/"] != 500 {
				t.Fatalf("FailOnce left = %v", fo)
			}
			if s, _ := get(t, srv, "/hrv-service/hrv/2026-10-07", access); s != 500 {
				t.Fatalf("second answer %d, want 500", s)
			}
			if s, _ := get(t, srv, "/hrv-service/hrv/2026-10-07", access); s != 404 {
				t.Fatalf("third answer %d, want 404", s)
			}
		}
	})
	t.Run("EP-483 FailOnce and a token the fake did not issue: 401, and FailOnce is not used up", func(t *testing.T) {
		t.Parallel()
		srv, access := signedIn(t)
		srv.Set(func(c *gconnecttest.Control) { c.FailOnce["/hrv-service/"] = 500 })
		if s, _ := get(t, srv, "/hrv-service/hrv/2026-10-07", "not-issued"); s != 401 {
			t.Fatalf("unknown token answered %d, want 401", s)
		}
		if s, _ := get(t, srv, "/hrv-service/hrv/2026-10-07", access); s != 500 {
			t.Fatalf("issued token answered %d, want the forced 500", s)
		}
	})
	t.Run("EP-484 a forced 429 carries Retry-After: 1; another forced status carries none", func(t *testing.T) {
		t.Parallel()
		srv, access := signedIn(t)
		srv.Set(func(c *gconnecttest.Control) { c.DataStatus["/a"] = 429; c.DataStatus["/b"] = 503 })
		for path, want := range map[string]string{"/a": "1", "/b": ""} {
			req, _ := http.NewRequest("GET", srv.URL()+path, nil)
			req.Header.Set("Authorization", "Bearer "+access)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if got := resp.Header.Get("Retry-After"); got != want {
				t.Errorf("%s: Retry-After %q, want %q", path, got, want)
			}
		}
	})
}

func TestHandle(t *testing.T) {
	t.Parallel()
	answer := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, body) }
	}
	t.Run("DT-R481 Handle: an exact path beats a prefix, the longest prefix wins, and no route is a 404", func(t *testing.T) {
		t.Parallel()
		srv, access := signedIn(t)
		srv.Handle("/x/", answer("short"))
		srv.Handle("/x/y/", answer("long"))
		srv.Handle("/x/y/z", answer("exact"))
		srv.Handle("/q", answer("q")) // no trailing slash: exact only
		cases := []struct {
			path   string
			status int
			body   string
		}{
			{"/x/y/z", 200, "exact"},
			{"/x/y/z/more", 200, "long"},
			{"/x/y/other", 200, "long"},
			{"/x/other", 200, "short"},
			{"/q", 200, "q"},
			{"/q/sub", 404, ""},
			{"/nothing", 404, ""},
		}
		for range 10 {
			for _, c := range cases {
				status, body := get(t, srv, c.path, access)
				if status != c.status || c.body != "" && body != c.body {
					t.Fatalf("%s: %d %q, want %d %q", c.path, status, body, c.status, c.body)
				}
			}
		}
	})
	t.Run("DT-R482 a Handle route still answers 401 to a token the fake did not issue, and a forced status still applies", func(t *testing.T) {
		t.Parallel()
		srv, access := signedIn(t)
		srv.Handle("/x", answer("ok"))
		if s, _ := get(t, srv, "/x", "not-issued"); s != 401 {
			t.Fatalf("unknown token answered %d", s)
		}
		srv.Set(func(c *gconnecttest.Control) { c.DataStatus["/x"] = 503 })
		if s, _ := get(t, srv, "/x", access); s != 503 {
			t.Fatalf("forced status answered %d", s)
		}
	})
	t.Run("DT-R483 diauth: a refresh token the fake did not issue or a ticket without ST-: 400 invalid_grant", func(t *testing.T) {
		t.Parallel()
		srv := gconnecttest.New()
		defer srv.Close()
		for _, form := range []string{"grant_type=refresh_token&refresh_token=other", "grant_type=x&service_ticket=TICKET"} {
			resp, err := http.Post(srv.URL()+"/di-oauth2-service/oauth/token", "application/x-www-form-urlencoded", strings.NewReader(form))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 400 || !strings.Contains(string(b), "invalid_grant") {
				t.Fatalf("%s: %d %s", form, resp.StatusCode, b)
			}
		}
	})
	t.Run("CL-484 the social profile without a route: a default display name", func(t *testing.T) {
		t.Parallel()
		srv, access := signedIn(t)
		status, body := get(t, srv, "/userprofile-service/socialProfile", access)
		if status != 200 || !strings.Contains(body, `"displayName":"displayName-redacted"`) {
			t.Fatalf("%d %s", status, body)
		}
	})
}

func TestCallsAndHelpers(t *testing.T) {
	t.Parallel()
	t.Run("CL-485 two requests and a sign in: Calls records method, path, query, headers and body, filtered by path", func(t *testing.T) {
		t.Parallel()
		srv, access := signedIn(t)
		srv.Reset()
		get(t, srv, "/a?x=1", access)
		get(t, srv, "/b", access)
		a := srv.Calls("/a")
		if len(a) != 1 || a[0].Method != "GET" || a[0].Query != "x=1" || a[0].Header.Get("Authorization") != "Bearer "+access {
			t.Fatalf("calls to /a = %+v", a)
		}
		if n := len(srv.Calls("")); n != 2 {
			t.Fatalf("%d calls, want 2", n)
		}
		logins := 0
		srv2, _ := signedIn(t)
		for _, c := range srv2.Calls("/mobile/api/login") {
			if strings.Contains(c.Body, `"password":"right-password"`) {
				logins++
			}
		}
		if logins != 1 {
			t.Fatalf("the login body was not recorded")
		}
	})
	t.Run("CL-486 JWT of a claims map: header.payload.sig with the claims as base64url JSON", func(t *testing.T) {
		t.Parallel()
		parts := strings.Split(gconnecttest.JWT(map[string]any{"exp": 5, "client_id": "X"}), ".")
		if len(parts) != 3 || parts[2] != "sig" {
			t.Fatalf("parts = %v", parts)
		}
		b, err := base64.RawURLEncoding.DecodeString(parts[1])
		var c struct {
			Exp      int    `json:"exp"`
			ClientID string `json:"client_id"`
		}
		if err != nil || json.Unmarshal(b, &c) != nil || c.Exp != 5 || c.ClientID != "X" {
			t.Fatalf("payload %s: %v", b, err)
		}
	})
	t.Run("CL-487 Hosts of a fake: all four hosts are its URL", func(t *testing.T) {
		t.Parallel()
		srv := gconnecttest.New()
		defer srv.Close()
		if h := srv.Hosts(); h != (gconnect.Hosts{SSO: srv.URL(), DIAuth: srv.URL(), ConnectAPI: srv.URL(), Consumer: srv.URL()}) {
			t.Fatalf("hosts = %+v", h)
		}
	})
	t.Run("EG-481 Set, Control, Handle and Calls while requests are in flight: no data race", func(t *testing.T) {
		t.Parallel()
		srv, access := signedIn(t)
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 20 {
					srv.Set(func(c *gconnecttest.Control) { c.FailOnce["/r"] = 500 })
					_ = srv.Control()
					srv.Handle("/r", func(w http.ResponseWriter, _ *http.Request) {})
					_ = srv.Calls("/r")
					if i%2 == 0 { // t.Fatal may not run off the test goroutine, so no get()
						req, _ := http.NewRequest("GET", srv.URL()+"/r", nil)
						req.Header.Set("Authorization", "Bearer "+access)
						if resp, err := http.DefaultClient.Do(req); err == nil {
							resp.Body.Close()
						}
					}
				}
			}()
		}
		wg.Wait()
	})
	t.Run("EG-482 a request to 192.0.2.1 in a test: the guard refuses it before any packet leaves", func(t *testing.T) {
		t.Parallel()
		_, err := http.Get("http://192.0.2.1/") // TEST-NET-1, RFC 5737
		if err == nil || !strings.Contains(err.Error(), "test guard") {
			t.Fatalf("err = %v", err)
		}
	})
}
