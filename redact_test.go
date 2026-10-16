package gconnect_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/danihrndzld/garmin-health-exporter/tauri-app/core/gconnect"
)

func TestRedactURL(t *testing.T) {
	cases := []struct{ id, in, want string }{
		{"CL-341 ports client.test.js redactUrl: token", "https://x.test/a?token=abc&date=2026-04-15", "https://x.test/a?token=REDACTED&date=2026-04-15"},
		{"CL-342 ports client.test.js redactUrl: access_token", "https://x.test/a?access_token=xyz&id=7", "https://x.test/a?access_token=REDACTED&id=7"},
		{"CL-343 ports client.test.js redactUrl: auth and password substrings", "https://x.test/a?authCode=xyz&password=hunter2&x=1", "https://x.test/a?authCode=REDACTED&password=REDACTED&x=1"},
		{"CL-344 ports client.test.js redactUrl: nothing secret", "https://x.test/a?date=2026-04-15", "https://x.test/a?date=2026-04-15"},
		{"CL-345 ports redact.test.js: every secret key, keep stays", "https://api.example.com/x?token=abc&access_token=def&authCode=ghi&secret=s&password=p&refresh_token=rt&sig=zz&keep=ok",
			"https://api.example.com/x?token=REDACTED&access_token=REDACTED&authCode=REDACTED&secret=REDACTED&password=REDACTED&refresh_token=REDACTED&sig=REDACTED&keep=ok"},
		{"CL-346 ports redact.test.js: empty and plain text stay", "", ""},
		{"CL-347 ports redact.test.js: plain text stays", "plain text", "plain text"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			if got := gconnect.RedactURL(c.in); got != c.want {
				t.Errorf("got %s", got)
			}
		})
	}
}

func TestRedact(t *testing.T) {
	cases := []struct {
		id, in    string
		match     []string // regexps the output must match
		forbidden []string // substrings the output must not hold
	}{
		{"CL-348 ports redact.test.js: Bearer with Authorization", "Authorization: Bearer eyJ0.abc.def", []string{`Bearer REDACTED`}, []string{"eyJ0"}},
		{"CL-349 ports redact.test.js: bearer in any case", "auth: bearer abc123.def-ghi", []string{`(?i)bearer REDACTED`}, []string{"abc123"}},
		{"CL-350 ports redact.test.js: Basic", "Authorization: Basic dXNlcjpwYXNz", []string{`Basic REDACTED`}, []string{"dXNlcjpwYXNz"}},
		{"CL-351 ports redact.test.js: Cookie", "Cookie: SESSIONID=abc; other=xyz", []string{`(?i)Cookie: REDACTED`}, []string{"SESSIONID"}},
		{"CL-352 ports redact.test.js: Set-Cookie", "Set-Cookie: JWT=eyJ...; Path=/", []string{`(?i)Set-Cookie: REDACTED`}, []string{"Path"}},
		{"CL-353 ports redact.test.js: a JWT anywhere", "error with token eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.sig embedded", []string{`REDACTED_JWT`}, []string{"eyJhbGciOi"}},
		{"CL-354 ports redact.test.js: JSON pairs with secret keys", `{"password":"hunter2","token":"abc","keep":"ok"}`, []string{`"password":"REDACTED"`, `"token":"REDACTED"`, `"keep":"ok"`}, []string{"hunter2"}},
		{"CL-355 ports redact.test.js: a URL inside a sentence", "GET https://x/y?access_token=abc&name=me failed", []string{`access_token=REDACTED`, `name=me`}, nil},
		{"CL-356 ports redact.test.js: isSecretKey variants through JSON pairs", `{"Access_Token":"a","clientSecret":"b","X-Auth-Token":"c","jwt":"d","username":"u","date":"2026-10-07"}`,
			[]string{`"Access_Token":"REDACTED"`, `"clientSecret":"REDACTED"`, `"X-Auth-Token":"REDACTED"`, `"jwt":"REDACTED"`, `"username":"u"`, `"date":"2026-10-07"`}, nil},
		{"CL-357 ports redact.test.js: empty stays empty", "", []string{`^$`}, nil},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
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
