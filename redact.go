package gconnect

import (
	"regexp"
	"slices"
	"strings"
)

// Keys whose values are secret: an exact name, or a name that holds one of the substrings.
var (
	secretQueryKeys  = []string{"token", "access_token", "refresh_token", "id_token", "code", "sig", "signature", "key", "apikey", "api_key", "jwt", "sessionid", "session_id", "authorization"}
	secretSubstrings = []string{"auth", "secret", "password", "passwd", "pwd"}
)

// IsSecretKey reports whether a query or JSON key holds a secret: an exact name such as token,
// code or jwt, or a name containing auth, secret, password, passwd or pwd. Case does not
// matter. Redact and RedactURL use it; callers can use it to scrub their own key and value pairs.
func IsSecretKey(key string) bool {
	k := strings.ToLower(key)
	if slices.Contains(secretQueryKeys, k) {
		return true
	}
	for _, s := range secretSubstrings {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

var (
	urlParamRE   = regexp.MustCompile(`([?&])([^=&]+)=([^&]*)`)
	authHeaderRE = regexp.MustCompile(`(?i)(Authorization:\s*)(Bearer|Basic)\s+\S+`)
	bearerRE     = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]+`)
	cookieRE     = regexp.MustCompile(`(?i)((?:set-)?cookie:\s*)[^\r\n]+`)
	jwtRE        = regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	jsonPairRE   = regexp.MustCompile(`"([^"]+)"\s*:\s*"([^"]*)"`)
	textParamRE  = regexp.MustCompile(`([?&])([^=&\s"']+)=([^&\s"']*)`)
)

// redactParams replaces the value of every secret key that re matches with REDACTED.
func redactParams(re *regexp.Regexp, s string) string {
	return re.ReplaceAllStringFunc(s, func(m string) string {
		g := re.FindStringSubmatch(m)
		if IsSecretKey(g[2]) {
			return g[1] + g[2] + "=REDACTED"
		}
		return m
	})
}

// RedactURL replaces the value of every secret query parameter in u with REDACTED.
func RedactURL(u string) string { return redactParams(urlParamRE, u) }

// Redact hides secrets in free text: Bearer and Basic values, cookie headers,
// JWT shaped strings, JSON pairs with secret keys, and secret query values.
func Redact(s string) string {
	s = authHeaderRE.ReplaceAllString(s, "${1}${2} REDACTED")
	s = bearerRE.ReplaceAllString(s, "${1} REDACTED")
	s = cookieRE.ReplaceAllString(s, "${1}REDACTED")
	s = jwtRE.ReplaceAllString(s, "REDACTED_JWT")
	s = jsonPairRE.ReplaceAllStringFunc(s, func(m string) string {
		if key := jsonPairRE.FindStringSubmatch(m)[1]; IsSecretKey(key) {
			return `"` + key + `":"REDACTED"`
		}
		return m
	})
	return redactParams(textParamRE, s)
}
