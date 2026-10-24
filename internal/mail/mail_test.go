package mail

import (
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
)

func TestBuild(t *testing.T) {
	from, _ := mail.ParseAddress("FilamentManager <noreply@example.fr>")
	raw, err := build(from, Message{To: "alice@example.fr", Subject: "Ton code de connexion é", Text: "Bonjour,\nvoici le lien : https://example.fr/auth/verify?t=abc"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("To") != "alice@example.fr" || !strings.Contains(msg.Header.Get("Message-ID"), "@example.fr>") {
		t.Errorf("en-têtes : %v", msg.Header)
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if subject != "Ton code de connexion é" {
		t.Errorf("sujet = %q", subject)
	}
	body, _ := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if !strings.Contains(string(body), "https://example.fr/auth/verify?t=abc") {
		t.Errorf("corps = %q", body)
	}
}
