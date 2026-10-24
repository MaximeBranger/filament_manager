// Package auth fournit les primitives d'authentification : jetons, codes, limitation de débit
// et détermination de l'IP cliente derrière un proxy.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// NewToken renvoie un jeton aléatoire de 32 octets encodé pour une URL.
func NewToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken renvoie l'empreinte SHA-256 stockée en base à la place du jeton.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewCode renvoie un code à 6 chiffres uniformément aléatoire.
func NewCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("%06d", n.Int64())
}

// HashCode lie le code à son défi et au secret serveur : un code de 6 chiffres seul serait
// trivial à retrouver depuis une simple empreinte.
func HashCode(secret []byte, challengeKey, code string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(challengeKey + ":" + code))
	return hex.EncodeToString(m.Sum(nil))
}

// Equal compare deux empreintes en temps constant.
func Equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// Sign et Verify protègent une valeur stockée côté client (cookie) contre la falsification.
func Sign(secret []byte, value string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(value))
	return value + "." + base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func Verify(secret []byte, signed string) (string, bool) {
	i := strings.LastIndexByte(signed, '.')
	if i < 0 {
		return "", false
	}
	value := signed[:i]
	return value, hmac.Equal([]byte(Sign(secret, value)), []byte(signed))
}

// RateLimiter compte les événements par clé sur une fenêtre glissante, en mémoire.
type RateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	events map[string][]time.Time
	Now    func() time.Time
	calls  int
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, events: map[string][]time.Time{}, Now: time.Now}
}

// Allow enregistre un événement pour key et indique s'il reste sous la limite.
func (r *RateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Now()
	cutoff := now.Add(-r.window)

	r.calls++
	if r.calls%1000 == 0 {
		for k, ev := range r.events {
			if len(ev) == 0 || !ev[len(ev)-1].After(cutoff) {
				delete(r.events, k)
			}
		}
	}

	kept := r.events[key][:0]
	for _, t := range r.events[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= r.limit {
		r.events[key] = kept
		return false
	}
	r.events[key] = append(kept, now)
	return true
}

// ClientIP renvoie l'IP du client. X-Forwarded-For n'est lu que si la connexion vient d'un
// proxy de confiance ; on prend alors la première adresse non fiable en partant de la droite.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	remote, err := netip.ParseAddr(host)
	if err != nil || !isTrusted(remote.Unmap(), trusted) {
		return host
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		if !isTrusted(addr.Unmap(), trusted) {
			return addr.Unmap().String()
		}
	}
	return host
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
