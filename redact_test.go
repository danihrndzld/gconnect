package gconnect_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/danihrndzld/gconnect"
)

func TestRedactURL(t *testing.T) {
	t.Parallel()
	cases := []struct{ id, in, want string }{
		{"CL-441 token=abc in a URL: token=REDACTED, date kept", "https://x.test/a?token=abc&date=2026-04-15", "https://x.test/a?token=REDACTED&date=2026-04-15"},
		{"CL-442 access_token=xyz: REDACTED, id kept", "https://x.test/a?access_token=xyz&id=7", "https://x.test/a?access_token=REDACTED&id=7"},
		{"CL-443 authCode and password, keys that hold a secret word: REDACTED", "https://x.test/a?authCode=xyz&password=hunter2&x=1", "https://x.test/a?authCode=REDACTED&password=REDACTED&x=1"},
		{"CL-444 a URL with nothing secret: unchanged", "https://x.test/a?date=2026-04-15", "https://x.test/a?date=2026-04-15"},
		{"CL-445 every secret key in one URL: each REDACTED, keep kept", "https://api.example.com/x?token=abc&access_token=def&authCode=ghi&secret=s&password=p&refresh_token=rt&sig=zz&keep=ok",
			"https://api.example.com/x?token=REDACTED&access_token=REDACTED&authCode=REDACTED&secret=REDACTED&password=REDACTED&refresh_token=REDACTED&sig=REDACTED&keep=ok"},
		{"CL-446 an empty string: empty", "", ""},
		{"CL-447 plain text: unchanged", "plain text", "plain text"},
		{"EP-446 an exact secret name in upper case, TOKEN: REDACTED", "https://x/a?TOKEN=abc", "https://x/a?TOKEN=REDACTED"},
		{"EP-447 a name that only holds a secret word, user_pwd: REDACTED", "https://x/a?user_pwd=abc", "https://x/a?user_pwd=REDACTED"},
		{"EP-448 names near a secret name, zipcode and keyboard: unchanged", "https://x/a?zipcode=1&keyboard=2", "https://x/a?zipcode=1&keyboard=2"},
		{"EP-449 a secret key with an empty value: REDACTED", "https://x/a?code=&x=1", "https://x/a?code=REDACTED&x=1"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			if got := gconnect.RedactURL(c.in); got != c.want {
				t.Errorf("got %s", got)
			}
		})
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id, in    string
		match     []string // regexps the output must match
		forbidden []string // substrings the output must not hold
	}{
		{"CL-448 Authorization: Bearer with a token: Bearer REDACTED", "Authorization: Bearer eyJ0.abc.def", []string{`Bearer REDACTED`}, []string{"eyJ0"}},
		{"CL-449 bearer in lower case: bearer REDACTED", "auth: bearer abc123.def-ghi", []string{`(?i)bearer REDACTED`}, []string{"abc123"}},
		{"CL-450 Authorization: Basic: Basic REDACTED", "Authorization: Basic dXNlcjpwYXNz", []string{`Basic REDACTED`}, []string{"dXNlcjpwYXNz"}},
		{"CL-451 a Cookie header: Cookie: REDACTED", "Cookie: SESSIONID=abc; other=xyz", []string{`(?i)Cookie: REDACTED`}, []string{"SESSIONID"}},
		{"CL-452 a Set-Cookie header: Set-Cookie: REDACTED", "Set-Cookie: JWT=eyJ...; Path=/", []string{`(?i)Set-Cookie: REDACTED`}, []string{"Path"}},
		{"CL-453 a JWT inside a sentence: REDACTED_JWT", "error with token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.sig embedded", []string{`REDACTED_JWT`}, []string{"eyJhbGciOi"}},
		{"CL-454 JSON pairs with secret keys: values REDACTED, keep kept", `{"password":"hunter2","token":"abc","keep":"ok"}`, []string{`"password":"REDACTED"`, `"token":"REDACTED"`, `"keep":"ok"`}, []string{"hunter2"}},
		{"CL-455 a URL inside a sentence: access_token=REDACTED, name kept", "GET https://x/y?access_token=abc&name=me failed", []string{`access_token=REDACTED`, `name=me`}, nil},
		{"CL-456 JSON keys in mixed case and with secret words: REDACTED, username and date kept", `{"Access_Token":"a","clientSecret":"b","X-Auth-Token":"c","jwt":"d","username":"u","date":"2026-10-07"}`,
			[]string{`"Access_Token":"REDACTED"`, `"clientSecret":"REDACTED"`, `"X-Auth-Token":"REDACTED"`, `"jwt":"REDACTED"`, `"username":"u"`, `"date":"2026-10-07"`}, nil},
		{"CL-457 an empty string: empty", "", []string{`^$`}, nil},
		{"EP-450 a cookie on one line of two: only that line REDACTED", "cookie: a=1\nnext line stays", []string{`(?i)cookie: REDACTED\nnext line stays`}, []string{"a=1"}},
		{"EP-451 a JSON pair with spaces around the colon: REDACTED", `{"refresh_token" : "rt-1"}`, []string{`"refresh_token":"REDACTED"`}, []string{"rt-1"}},
		{"EP-452 text with no secret: unchanged", "HTTP 500: upstream timed out", []string{`^HTTP 500: upstream timed out$`}, nil},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			t.Parallel()
			got := gconnect.Redact(c.in)
			for _, m := range c.match {
				if !regexp.MustCompile(m).MatchString(got) {
					t.Errorf("%q does not match %s", got, m)
				}
			}
			for _, f := range c.forbidden {
				if strings.Contains(got, f) {
					t.Errorf("%q still holds %q", got, f)
				}
			}
		})
	}
}

func TestIsSecretKey(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, key string
		want      bool
	}{
		{"EP-485 an exact secret name is secret", "token", true},
		{"EP-486 case does not matter", "Access_Token", true},
		{"EP-487 a name containing secret is secret", "clientSecret", true},
		{"EP-488 a name containing auth is secret", "X-Auth-Token", true},
		{"EP-489 jwt is secret", "jwt", true},
		{"EP-490 code is secret", "code", true},
		{"EP-491 an ordinary name is not secret", "username", false},
		{"EP-492 a date key is not secret", "date", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := gconnect.IsSecretKey(c.key); got != c.want {
				t.Errorf("IsSecretKey(%q) = %v, want %v", c.key, got, c.want)
			}
		})
	}
}
