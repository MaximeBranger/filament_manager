package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func validEnv() map[string]string {
	return map[string]string{
		"BASE_URL":       "https://filaments.example.fr/",
		"SESSION_SECRET": strings.Repeat("x", 32),
		"SMTP_HOST":      "smtp.example.fr",
		"SMTP_FROM":      "FilamentManager <noreply@example.fr>",
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(env(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.BaseURL.String() != "https://filaments.example.fr" {
		t.Errorf("BaseURL = %s", c.BaseURL)
	}
	if !c.SecureCookies() {
		t.Error("cookies sécurisés attendus en https")
	}
	if c.ListenAddr != ":8080" || c.DatabasePath != "/data/app.db" || c.SMTPPort != 587 {
		t.Errorf("valeurs par défaut inattendues : %+v", c)
	}
	if c.RateLimitEmail != (Rate{3, 15 * time.Minute}) {
		t.Errorf("RateLimitEmail = %+v", c.RateLimitEmail)
	}
	if c.PlausibleEnabled() {
		t.Error("Plausible ne doit pas être actif par défaut")
	}
}

func TestLoadMissingRequired(t *testing.T) {
	_, err := Load(env(map[string]string{}))
	if err == nil {
		t.Fatal("erreur attendue")
	}
	for _, key := range []string{"BASE_URL", "SESSION_SECRET", "SMTP_HOST", "SMTP_FROM"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("l'erreur devrait mentionner %s : %v", key, err)
		}
	}
}

func TestLoadInvalidValues(t *testing.T) {
	e := validEnv()
	e["SESSION_SECRET"] = "court"
	e["SMTP_TLS"] = "ssl"
	e["RATE_LIMIT_IP"] = "beaucoup"
	e["TRUSTED_PROXIES"] = "172.18.0.0/16, pas-une-ip"
	_, err := Load(env(e))
	if err == nil {
		t.Fatal("erreur attendue")
	}
	for _, key := range []string{"SESSION_SECRET", "SMTP_TLS", "RATE_LIMIT_IP", "TRUSTED_PROXIES"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("l'erreur devrait mentionner %s : %v", key, err)
		}
	}
}

func TestTrustedProxies(t *testing.T) {
	e := validEnv()
	e["TRUSTED_PROXIES"] = "172.18.0.0/16,10.0.0.5"
	c, err := Load(env(e))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.TrustedProxies) != 2 || c.TrustedProxies[1].String() != "10.0.0.5/32" {
		t.Errorf("TrustedProxies = %v", c.TrustedProxies)
	}
}

func TestShareSettings(t *testing.T) {
	c, err := Load(env(validEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if c.ShareDir != "/data/shares" || c.ShareMaxFileSize != 50<<20 || c.ShareMaxActive != 5 {
		t.Errorf("valeurs par défaut : %s %d %d", c.ShareDir, c.ShareMaxFileSize, c.ShareMaxActive)
	}
	e := validEnv()
	e["SHARE_MAX_FILE_SIZE"] = "nope"
	e["SHARE_MAX_ACTIVE"] = "0"
	if _, err := Load(env(e)); err == nil || !strings.Contains(err.Error(), "SHARE_MAX_FILE_SIZE") || !strings.Contains(err.Error(), "SHARE_MAX_ACTIVE") {
		t.Errorf("erreurs attendues : %v", err)
	}
	for in, want := range map[string]int64{"2048": 2048, "512KB": 512 << 10, "50MB": 50 << 20, "1 gb": 1 << 30} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v", in, got, err)
		}
	}
}

func TestParseRate(t *testing.T) {
	r, err := ParseRate("5/1h")
	if err != nil || r.Count != 5 || r.Window != time.Hour {
		t.Errorf("ParseRate = %+v, %v", r, err)
	}
	for _, bad := range []string{"5", "0/1h", "x/1h", "5/x"} {
		if _, err := ParseRate(bad); err == nil {
			t.Errorf("ParseRate(%q) devrait échouer", bad)
		}
	}
}
