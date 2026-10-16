package gconnect_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
)

// TestMain lets the tests dial loopback addresses only, so no test can reach the real Garmin,
// which locks an account after a few sign ins.
func TestMain(m *testing.M) {
	t := http.DefaultTransport.(*http.Transport).Clone()
	dial := t.DialContext
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if ip := net.ParseIP(host); err != nil || host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("test guard: %s is not a loopback address", addr)
		}
		return dial(ctx, network, addr)
	}
	http.DefaultTransport = t
	os.Exit(m.Run())
}
