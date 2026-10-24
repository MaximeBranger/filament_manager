package auth

import (
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestTokensAndCodes(t *testing.T) {
	a, b := NewToken(), NewToken()
	if a == b || len(a) < 40 {
		t.Errorf("jetons suspects : %q %q", a, b)
	}
	if HashToken(a) == a || HashToken(a) != HashToken(a) {
		t.Error("HashToken incohérent")
	}
	for range 100 {
		if c := NewCode(); len(c) != 6 {
			t.Fatalf("code %q", c)
		}
	}
	secret := []byte("secret")
	if HashCode(secret, "1", "123456") == HashCode(secret, "2", "123456") {
		t.Error("le hash du code doit dépendre du défi")
	}
}

func TestSignVerify(t *testing.T) {
	secret := []byte("s3cret")
	signed := Sign(secret, "alice@example.fr|123")
	if v, ok := Verify(secret, signed); !ok || v != "alice@example.fr|123" {
		t.Errorf("Verify = %q, %v", v, ok)
	}
	if _, ok := Verify(secret, signed+"x"); ok {
		t.Error("signature altérée acceptée")
	}
	if _, ok := Verify([]byte("autre"), signed); ok {
		t.Error("signature d'un autre secret acceptée")
	}
}

func TestRateLimiter(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	r := NewRateLimiter(3, 15*time.Minute)
	r.Now = func() time.Time { return now }
	for i := range 3 {
		if !r.Allow("a") {
			t.Fatalf("appel %d refusé", i)
		}
	}
	if r.Allow("a") {
		t.Error("4e appel accepté")
	}
	if !r.Allow("b") {
		t.Error("clé indépendante refusée")
	}
	now = now.Add(16 * time.Minute)
	if !r.Allow("a") {
		t.Error("appel refusé après la fenêtre")
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16")}

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := ClientIP(r, trusted); got != "203.0.113.9" {
		t.Errorf("XFF d'une source non fiable lu : %s", got)
	}

	r.RemoteAddr = "172.18.0.2:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 198.51.100.7")
	if got := ClientIP(r, trusted); got != "198.51.100.7" {
		t.Errorf("IP derrière le proxy = %s", got)
	}

	r.Header.Del("X-Forwarded-For")
	if got := ClientIP(r, trusted); got != "172.18.0.2" {
		t.Errorf("sans XFF = %s", got)
	}
}
