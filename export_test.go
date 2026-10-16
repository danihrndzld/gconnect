package gconnect

// The tests live in gconnect_test, because gconnecttest imports gconnect.
var (
	JWTClaims  = jwtClaims
	SignOAuth1 = signOAuth1
	RetryAfter = retryAfter
)

// HostURLs returns the four hosts h resolves to: SSO, DIAuth, ConnectAPI and Consumer.
func HostURLs(h Hosts) [4]string { return [4]string{h.sso(), h.diauth(), h.connectAPI(), h.consumer()} }
