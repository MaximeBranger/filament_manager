// Package mail envoie les emails de l'application.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Message struct {
	To      string
	Subject string
	Text    string
}

type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// SMTP envoie les emails via un serveur SMTP.
type SMTP struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	TLS      string // starttls | tls | none
}

func (s *SMTP) Send(ctx context.Context, m Message) error {
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("SMTP_FROM invalide : %w", err)
	}
	body, err := build(from, m)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	tlsConfig := &tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}

	var conn net.Conn
	if s.TLS == "tls" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: tlsConfig}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	} else {
		conn.SetDeadline(time.Now().Add(30 * time.Second))
	}

	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()

	if s.TLS == "starttls" {
		if err := c.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("STARTTLS : %w", err)
		}
	}
	if s.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.User, s.Password, s.Host)); err != nil {
			return fmt.Errorf("authentification SMTP : %w", err)
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	if err := c.Rcpt(m.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func build(from *mail.Address, m Message) ([]byte, error) {
	var buf bytes.Buffer
	id := make([]byte, 16)
	rand.Read(id)
	domain := "localhost"
	if _, d, ok := strings.Cut(from.Address, "@"); ok {
		domain = d
	}
	headers := []struct{ k, v string }{
		{"From", from.String()},
		{"To", m.To},
		{"Subject", mime.QEncoding.Encode("utf-8", m.Subject)},
		{"Date", time.Now().Format(time.RFC1123Z)},
		{"Message-ID", "<" + hex.EncodeToString(id) + "@" + domain + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", "text/plain; charset=utf-8"},
		{"Content-Transfer-Encoding", "quoted-printable"},
		{"Auto-Submitted", "auto-generated"},
	}
	for _, h := range headers {
		fmt.Fprintf(&buf, "%s: %s\r\n", h.k, h.v)
	}
	buf.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&buf)
	if _, err := qp.Write([]byte(m.Text)); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Fake capture les emails au lieu de les envoyer (tests et développement).
type Fake struct {
	mu   sync.Mutex
	Sent []Message
}

func (f *Fake) Send(_ context.Context, m Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = append(f.Sent, m)
	return nil
}

// Last renvoie le dernier email envoyé à to.
func (f *Fake) Last(to string) (Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.Sent) - 1; i >= 0; i-- {
		if f.Sent[i].To == to {
			return f.Sent[i], true
		}
	}
	return Message{}, false
}

func (f *Fake) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Sent)
}
