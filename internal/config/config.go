// Package config lit et valide la configuration fournie par variables d'environnement.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Rate décrit une limite « N événements par fenêtre ».
type Rate struct {
	Count  int
	Window time.Duration
}

type Config struct {
	BaseURL      *url.URL
	ListenAddr   string
	DatabasePath string

	SessionSecret []byte
	SessionTTL    time.Duration

	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string
	SMTPTLS      string

	MagicLinkTTL   time.Duration
	RateLimitEmail Rate
	RateLimitIP    Rate
	TrustedProxies []netip.Prefix

	BackupDir       string
	BackupInterval  time.Duration
	BackupRetention int

	PlausibleURL    string
	PlausibleDomain string

	LogLevel string

	ShareDir         string
	ShareMaxFileSize int64
	ShareMaxActive   int

	WebAuthnRPID   string
	WebAuthnRPName string
}

// Origin renvoie l'origine publique (schéma + hôte) de l'application.
func (c *Config) Origin() string { return c.BaseURL.Scheme + "://" + c.BaseURL.Host }

// SecureCookies indique si les cookies doivent porter l'attribut Secure.
func (c *Config) SecureCookies() bool { return c.BaseURL.Scheme == "https" }

// PlausibleEnabled indique si le script de statistiques doit être injecté.
func (c *Config) PlausibleEnabled() bool { return c.PlausibleURL != "" && c.PlausibleDomain != "" }

// Load construit la configuration à partir d'une fonction de lecture (os.Getenv en production).
// Toutes les erreurs sont collectées pour être affichées d'un coup.
func Load(getenv func(string) string) (*Config, error) {
	p := parser{getenv: getenv}
	c := &Config{}

	if raw := p.required("BASE_URL"); raw != "" {
		u, err := url.Parse(strings.TrimRight(raw, "/"))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			p.fail("BASE_URL", "doit être une URL absolue http(s)")
		} else {
			c.BaseURL = u
		}
	}
	c.ListenAddr = p.str("LISTEN_ADDR", ":8080")
	c.DatabasePath = p.str("DATABASE_PATH", "/data/app.db")

	if s := p.required("SESSION_SECRET"); s != "" {
		if len(s) < 32 {
			p.fail("SESSION_SECRET", "doit faire au moins 32 caractères")
		}
		c.SessionSecret = []byte(s)
	}
	c.SessionTTL = p.duration("SESSION_TTL", 720*time.Hour)

	c.SMTPHost = p.required("SMTP_HOST")
	c.SMTPPort = p.integer("SMTP_PORT", 587)
	c.SMTPUser = p.str("SMTP_USER", "")
	c.SMTPPassword = p.str("SMTP_PASSWORD", "")
	c.SMTPFrom = p.required("SMTP_FROM")
	c.SMTPTLS = p.str("SMTP_TLS", "starttls")
	switch c.SMTPTLS {
	case "starttls", "tls", "none":
	default:
		p.fail("SMTP_TLS", "valeurs possibles : starttls, tls, none")
	}

	c.MagicLinkTTL = p.duration("MAGIC_LINK_TTL", 15*time.Minute)
	c.RateLimitEmail = p.rate("RATE_LIMIT_EMAIL", Rate{3, 15 * time.Minute})
	c.RateLimitIP = p.rate("RATE_LIMIT_IP", Rate{20, 15 * time.Minute})
	c.TrustedProxies = p.prefixes("TRUSTED_PROXIES")

	c.BackupDir = p.str("BACKUP_DIR", "")
	c.BackupInterval = p.duration("BACKUP_INTERVAL", 6*time.Hour)
	c.BackupRetention = p.integer("BACKUP_RETENTION", 28)
	if c.BackupRetention < 1 {
		p.fail("BACKUP_RETENTION", "doit être ≥ 1")
	}

	c.PlausibleURL = strings.TrimRight(p.str("PLAUSIBLE_URL", ""), "/")
	c.PlausibleDomain = p.str("PLAUSIBLE_DOMAIN", "")

	c.LogLevel = p.str("LOG_LEVEL", "info")
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		p.fail("LOG_LEVEL", "valeurs possibles : debug, info, warn, error")
	}

	c.ShareDir = p.str("SHARE_DIR", "/data/shares")
	c.ShareMaxFileSize = 50 << 20
	if raw := p.str("SHARE_MAX_FILE_SIZE", ""); raw != "" {
		size, err := ParseSize(raw)
		if err != nil || size < 1024 {
			p.fail("SHARE_MAX_FILE_SIZE", "taille invalide (ex. 50MB)")
		} else {
			c.ShareMaxFileSize = size
		}
	}
	c.ShareMaxActive = p.integer("SHARE_MAX_ACTIVE", 5)
	if c.ShareMaxActive < 1 {
		p.fail("SHARE_MAX_ACTIVE", "doit être ≥ 1")
	}

	c.WebAuthnRPName = p.str("WEBAUTHN_RP_NAME", "FilamentManager")
	if c.BaseURL != nil {
		host := c.BaseURL.Hostname()
		c.WebAuthnRPID = strings.ToLower(p.str("WEBAUTHN_RP_ID", host))
		// L'identifiant de la partie de confiance doit être le domaine de BASE_URL ou l'un de ses suffixes.
		if c.WebAuthnRPID != host && !strings.HasSuffix(host, "."+c.WebAuthnRPID) {
			p.fail("WEBAUTHN_RP_ID", "doit être le domaine de BASE_URL ou un domaine parent ("+host+")")
		}
	}

	if len(p.errs) > 0 {
		return nil, errors.Join(p.errs...)
	}
	return c, nil
}

type parser struct {
	getenv func(string) string
	errs   []error
}

func (p *parser) fail(key, msg string) {
	p.errs = append(p.errs, fmt.Errorf("%s : %s", key, msg))
}

func (p *parser) str(key, def string) string {
	if v := strings.TrimSpace(p.getenv(key)); v != "" {
		return v
	}
	return def
}

func (p *parser) required(key string) string {
	v := strings.TrimSpace(p.getenv(key))
	if v == "" {
		p.fail(key, "obligatoire")
	}
	return v
}

func (p *parser) integer(key string, def int) int {
	v := p.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.fail(key, "doit être un entier")
		return def
	}
	return n
}

func (p *parser) duration(key string, def time.Duration) time.Duration {
	v := p.str(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		p.fail(key, "doit être une durée positive (ex. 15m, 6h)")
		return def
	}
	return d
}

func (p *parser) rate(key string, def Rate) Rate {
	v := p.str(key, "")
	if v == "" {
		return def
	}
	r, err := ParseRate(v)
	if err != nil {
		p.fail(key, err.Error())
		return def
	}
	return r
}

func (p *parser) prefixes(key string) []netip.Prefix {
	var out []netip.Prefix
	for _, part := range strings.Split(p.str(key, ""), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			addr, err := netip.ParseAddr(part)
			if err != nil {
				p.fail(key, fmt.Sprintf("adresse invalide %q", part))
				continue
			}
			out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
			continue
		}
		pref, err := netip.ParsePrefix(part)
		if err != nil {
			p.fail(key, fmt.Sprintf("plage invalide %q", part))
			continue
		}
		out = append(out, pref.Masked())
	}
	return out
}

// ParseSize lit une taille en octets : "52428800", "512KB", "50MB", "1GB" (multiples de 1024).
func ParseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, errors.New("taille invalide")
	}
	return n * mult, nil
}

// ParseRate lit une limite au format "N/durée", par exemple "3/15m".
func ParseRate(s string) (Rate, error) {
	n, w, ok := strings.Cut(s, "/")
	if !ok {
		return Rate{}, errors.New(`format attendu "N/durée", ex. 3/15m`)
	}
	count, err := strconv.Atoi(strings.TrimSpace(n))
	if err != nil || count < 1 {
		return Rate{}, errors.New("le nombre doit être un entier ≥ 1")
	}
	window, err := time.ParseDuration(strings.TrimSpace(w))
	if err != nil || window <= 0 {
		return Rate{}, errors.New("durée invalide")
	}
	return Rate{Count: count, Window: window}, nil
}
