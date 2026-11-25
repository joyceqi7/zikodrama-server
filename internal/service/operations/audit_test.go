package operations

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPUsesGatewayHeadersBeforeDockerPeerAddress(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "172.17.0.1:45678"
	request.Header.Set("X-Real-IP", "203.0.113.8")
	request.Header.Set("X-Forwarded-For", "198.51.100.9, 203.0.113.7")

	if actual := clientIP(request); actual != "203.0.113.8" {
		t.Fatalf("clientIP() = %q, want %q", actual, "203.0.113.8")
	}
}

func TestClientIPFallsBackToFirstForwardedAddress(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "172.17.0.1:45678"
	request.Header.Set("X-Forwarded-For", "not-an-ip, 198.51.100.9, 203.0.113.7")

	if actual := clientIP(request); actual != "198.51.100.9" {
		t.Fatalf("clientIP() = %q, want %q", actual, "198.51.100.9")
	}
}

func TestClientIPRejectsInvalidProxyHeaders(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	request.RemoteAddr = "172.17.0.1:45678"
	request.Header.Set("X-Real-IP", "not-an-ip")
	request.Header.Set("X-Forwarded-For", "also-not-an-ip")

	if actual := clientIP(request); actual != "172.17.0.1" {
		t.Fatalf("clientIP() = %q, want %q", actual, "172.17.0.1")
	}
}
