package gconnect_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/danihrndzld/garmin-health-exporter/tauri-app/core/gconnect"
	"github.com/danihrndzld/garmin-health-exporter/tauri-app/core/gconnect/gconnecttest"
)

func fakeGarmin(t *testing.T) *gconnecttest.Server {
	t.Helper()
	srv := gconnecttest.New()
	t.Cleanup(srv.Close)
	return srv
}

func parse(t *testing.T, body string) *Tokens {
	t.Helper()
	var tok Tokens
	if err := json.Unmarshal([]byte(body), &tok); err != nil {
		t.Fatal(err)
	}
	return &tok
}

func posts(calls []gconnecttest.Call) int {
	n := 0
	for _, c := range calls {
		if c.Method == "POST" {
			n++
		}
	}
	return n
}

func TestLoginDecisionTable(t *testing.T) {
	cases := []struct {
		id          string
		set         func(*gconnecttest.Control)
		password    string
		wantCode    LoginCode // "" means success
		wantKind    string
		widgetPosts int
		diCalls     int // -1: not checked
	}{
		{"DT-R211 ports DT-R1: mobile and DI succeed", nil, "right-password", "", "di", 0, 1},
		{"DT-R212 ports DT-R2: wrong password stops the chain", nil, "nope", BadPassword, "", 0, 0},
		{"DT-R213 ports DT-R3: MFA stops the chain", func(c *gconnecttest.Control) { c.MobileLogin = "mfa" }, "right-password", MFARequired, "", 0, 0},
		{"DT-R214 ports DT-R4: 429 on mobile, legacy signs in", func(c *gconnecttest.Control) { c.MobileLogin = "429" }, "right-password", "", "legacy", 1, 0},
		{"DT-R215 ports DT-R5: Cloudflare 403 on mobile, legacy signs in", func(c *gconnecttest.Control) { c.MobileLogin = "cloudflare" }, "right-password", "", "legacy", 1, 0},
		{"DT-R216 ports DT-R6: captcha on mobile, legacy signs in", func(c *gconnecttest.Control) { c.MobileLogin = "captcha" }, "right-password", "", "legacy", 1, 0},
		{"DT-R217 ports DT-R7: DI refused for every client id, legacy signs in", func(c *gconnecttest.Control) { c.DIAuth = "fail" }, "right-password", "", "legacy", 1, 4},
		{"DT-R218 ports DT-R8: DI answers 429, no legacy", func(c *gconnecttest.Control) { c.DIAuth = "429" }, "right-password", RateLimited, "", 0, 1},
		{"DT-R220 ports DT-R10: both logins blocked (403)", func(c *gconnecttest.Control) { c.MobileLogin = "cloudflare"; c.WidgetStatus = 403 }, "right-password", Blocked, "", 1, 0},
	}
	srv := fakeGarmin(t)
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			srv.Reset()
			if c.set != nil {
				c.set(srv.Control())
			}
			var logs []string
			a := &Auth{Hosts: srv.Hosts(), Log: func(m string) { logs = append(logs, m) }}
			tok, err := a.Login(context.Background(), "e2e@example.invalid", c.password)
			if c.wantCode == "" {
				if err != nil {
					t.Fatalf("login failed: %v", err)
				}
				if want := map[string]Kind{"di": KindDI, "legacy": KindLegacy}[c.wantKind]; tok.Kind() != want {
					t.Fatalf("kind: got %v, want %v", tok.Kind(), want)
				}
			} else {
				var le *LoginError
				if !errors.As(err, &le) || le.Code != c.wantCode {
					t.Fatalf("got %v, want %s", err, c.wantCode)
				}
				if strings.Contains(le.Msg, c.password) {
					t.Fatal("the error holds the password")
				}
			}
			for _, l := range logs {
				if strings.Contains(l, c.password) {
					t.Fatalf("a log line holds the password: %q", l)
				}
			}
			if n := posts(srv.Calls("/sso/signin")); n != c.widgetPosts {
				t.Fatalf("widget sign in posts: %d, want %d", n, c.widgetPosts)
			}
			if c.widgetPosts == 0 && len(srv.Calls("/sso/embed")) != 0 {
				t.Fatal("a widget request was sent")
			}
			if n := len(srv.Calls("/mobile/api/login")); n != 1 {
				t.Fatalf("mobile logins: %d, want 1", n)
			}
			if n := len(srv.Calls("/di-oauth2-service/oauth/token")); c.diCalls >= 0 && n != c.diCalls {
				t.Fatalf("diauth calls: %d, want %d", n, c.diCalls)
			}
		})
	}
}

func TestFreshRenewsLegacyThroughOAuth1(t *testing.T) {
	t.Run("ST-T202 ports ST-T2: an expired legacy token renews through the OAuth1 exchange", func(t *testing.T) {
		srv := fakeGarmin(t)
		past := strconv.FormatInt(time.Now().Unix()-10, 10)
		tok := parse(t, `{"oauth1":{"oauth_token":"ot","oauth_token_secret":"os"},"oauth2":{"access_token":"old","expires_at":`+past+`,"refresh_token_expires_at":`+past+`},"consumer":{"consumer_key":"ck","consumer_secret":"cs"}}`)
		got, refreshed, err := (&Auth{Hosts: srv.Hosts()}).Fresh(context.Background(), tok, 15*time.Minute)
		if err != nil || !refreshed || got.OAuth2.AccessToken == "old" {
			t.Fatalf("ST-T202 ports ST-T2: %v", err)
		}
		if len(srv.Calls("/oauth-service/oauth/exchange/user/2.0")) != 1 || len(srv.Calls("/mobile/api/login")) != 0 {
			t.Fatal("ST-T202 the legacy token was not renewed through the OAuth1 exchange alone")
		}
	})
}

func TestRefreshWithoutRefreshTokenSendsNothing(t *testing.T) {
	srv := fakeGarmin(t)
	tok := parse(t, `{"di":{"access_token":"x","client_id":"GARMIN_CONNECT_MOBILE_ANDROID_DI_2025Q2","expires_at":1}}`)
	t.Run("ST-T211 a DI tokens.json without refresh_token is dead and sends nothing", func(t *testing.T) {
		_, _, err := (&Auth{Hosts: srv.Hosts()}).Fresh(context.Background(), tok, 15*time.Minute)
		if !errors.Is(err, ErrSessionDead) {
			t.Fatalf("got %v, want ErrSessionDead", err)
		}
		if n := len(srv.Calls("")); n != 0 {
			t.Fatalf("%d requests", n)
		}
	})
}

func TestJWTHelperReadable(t *testing.T) {
	t.Run("EG-221 ports EG-06: the JWT claims are readable", func(t *testing.T) {
		c := JWTClaims(gconnecttest.JWT(map[string]any{"exp": 5, "client_id": "X"}))
		if c.ClientID != "X" || c.Exp != 5 {
			t.Fatalf("EG-221 ports EG-06: got %+v", c)
		}
	})
}

func TestSignOAuth1MatchesAuthJs(t *testing.T) {
	t.Run("CL-222 the OAuth1 signature matches auth.js", func(t *testing.T) {
		params := map[string]string{
			"oauth_consumer_key": "ck", "oauth_nonce": "n", "oauth_signature_method": "HMAC-SHA1",
			"oauth_timestamp": "1", "oauth_version": "1.0", "ticket": "ST-1",
			"login-url": "https://sso.garmin.com/sso/embed", "accepts-mfa-tokens": "true",
		}
		// node -e "console.log(require('./electron-app/garmin/auth').signOAuth1(...))", see the task 3 brief.
		if got := SignOAuth1("GET", "https://x/oauth-service/oauth/preauthorized", params, "cs", ""); got != "vuDWLkAGwPDSl8r0q3ly059MkjE=" {
			t.Fatalf("CL-222 signature %s", got)
		}
	})
}

func TestHeadersByKind(t *testing.T) {
	t.Run("CL-223 DI tokens send the native headers", func(t *testing.T) {
		h := Headers(&Tokens{DI: &DI{AccessToken: "a"}})
		if h.Get("X-Garmin-Client-Platform") != "Android" || h.Get("User-Agent") != "GCM-Android-5.23" || h.Get("Authorization") != "Bearer a" {
			t.Fatalf("CL-223 DI headers %v", h)
		}
	})
	t.Run("CL-224 legacy tokens send the connectmobile user agent", func(t *testing.T) {
		h := Headers(&Tokens{OAuth1: &OAuth1{}, OAuth2: &OAuth2{AccessToken: "b"}, Consumer: &Consumer{}})
		if h.Get("User-Agent") != "com.garmin.android.apps.connectmobile" || h.Get("X-Garmin-Client-Platform") != "" || h.Get("Authorization") != "Bearer b" {
			t.Fatalf("CL-224 legacy headers %v", h)
		}
	})
}

func TestSocialProfileNeedsAnIssuedToken(t *testing.T) {
	t.Run("CL-225 the fake accepts only an issued token", func(t *testing.T) {
		srv := fakeGarmin(t)
		a := &Auth{Hosts: srv.Hosts()}
		tok, err := a.Login(context.Background(), "e2e@example.invalid", "right-password")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			access string
			want   int
		}{{tok.DI.AccessToken, 200}, {"unknown", 401}} {
			req, _ := http.NewRequestWithContext(context.Background(), "GET", srv.URL()+"/userprofile-service/socialProfile", nil)
			req.Header = Headers(&Tokens{DI: &DI{AccessToken: c.access}})
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != c.want {
				t.Fatalf("CL-225 token %q answered %d, want %d", c.access, resp.StatusCode, c.want)
			}
		}
	})
}
