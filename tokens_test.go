package gconnect_test

import (
	"encoding/json"
	"testing"

	"github.com/danihrndzld/gconnect"
)

func TestTokensKind(t *testing.T) {
	t.Parallel()
	di := &gconnect.DI{AccessToken: "a", ExpiresAt: 100}
	legacy := func() gconnect.Tokens {
		return gconnect.Tokens{OAuth1: &gconnect.OAuth1{Token: "t"}, OAuth2: &gconnect.OAuth2{AccessToken: "b", ExpiresAt: 200}, Consumer: &gconnect.Consumer{Key: "k"}}
	}
	both := legacy()
	both.DI = di
	emptyDI := legacy()
	emptyDI.DI = &gconnect.DI{}
	noConsumer := legacy()
	noConsumer.Consumer = nil
	cases := []struct {
		id      string
		tok     gconnect.Tokens
		kind    gconnect.Kind
		expires int64
	}{
		{"EP-413 no login at all: KindNone, expiry 0", gconnect.Tokens{}, gconnect.KindNone, 0},
		{"EP-414 a DI login with an access token: KindDI, the DI expiry", gconnect.Tokens{DI: di}, gconnect.KindDI, 100},
		{"EP-415 a DI login without an access token: KindNone", gconnect.Tokens{DI: &gconnect.DI{RefreshToken: "r"}}, gconnect.KindNone, 0},
		{"EP-416 OAuth1, OAuth2 and Consumer: KindLegacy, the OAuth2 expiry", legacy(), gconnect.KindLegacy, 200},
		{"EP-417 legacy without Consumer: KindNone, though OAuth2 has an expiry", noConsumer, gconnect.KindNone, 200},
		{"EP-418 a DI login and a legacy login: KindDI wins", both, gconnect.KindDI, 100},
		{"EP-419 an empty DI next to a legacy login: KindLegacy", emptyDI, gconnect.KindLegacy, 200},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			if k, e := c.tok.Kind(), c.tok.AccessExpiresAt(); k != c.kind || e != c.expires {
				t.Fatalf("Kind %v, AccessExpiresAt %d", k, e)
			}
		})
	}
}

func TestTokensJSON(t *testing.T) {
	t.Parallel()
	t.Run("CL-411 marshal a DI login: Garmin's field names and a null refresh expiry", func(t *testing.T) {
		t.Parallel()
		b, _ := json.Marshal(gconnect.Tokens{DI: &gconnect.DI{AccessToken: "a", RefreshToken: "r", ClientID: "c", ExpiresAt: 5}})
		if want := `{"di":{"access_token":"a","refresh_token":"r","client_id":"c","expires_at":5,"refresh_token_expires_at":null}}`; string(b) != want {
			t.Fatalf("got %s", b)
		}
	})
	t.Run("CL-412 marshal a legacy login: the garth field names and no di key", func(t *testing.T) {
		t.Parallel()
		b, _ := json.Marshal(gconnect.Tokens{OAuth1: &gconnect.OAuth1{Token: "t", Secret: "s"}, OAuth2: &gconnect.OAuth2{AccessToken: "a", TokenType: "Bearer", RefreshToken: "r", ExpiresAt: 1, RefreshTokenExpiresAt: 2}, Consumer: &gconnect.Consumer{Key: "k", Secret: "cs"}})
		want := `{"oauth1":{"oauth_token":"t","oauth_token_secret":"s"},"oauth2":{"access_token":"a","token_type":"Bearer","refresh_token":"r","expires_at":1,"refresh_token_expires_at":2},"consumer":{"consumer_key":"k","consumer_secret":"cs"}}`
		if string(b) != want {
			t.Fatalf("got %s", b)
		}
	})
	t.Run("CL-413 marshal then unmarshal: the same tokens and the same kind", func(t *testing.T) {
		t.Parallel()
		at := int64(9)
		in := gconnect.Tokens{DI: &gconnect.DI{AccessToken: "a", RefreshToken: "r", ClientID: "c", ExpiresAt: 5, RefreshTokenExpiresAt: &at}}
		b, _ := json.Marshal(in)
		var out gconnect.Tokens
		if err := json.Unmarshal(b, &out); err != nil || *out.DI.RefreshTokenExpiresAt != 9 || out.DI.AccessToken != "a" || out.Kind() != gconnect.KindDI {
			t.Fatalf("got %+v %v", out.DI, err)
		}
	})
}
