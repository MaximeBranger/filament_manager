package modelimport

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

var errForbiddenAddress = errors.New("adresse non autorisée : seuls les sites publics sont accessibles")

// cgnat (100.64.0.0/10) n'est pas couvert par netip.Addr.IsPrivate.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// publicAddr indique si une adresse est joignable publiquement (pas de réseau local, de boucle, etc.).
func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a)
}

// newClient crée le client HTTP des imports. Le contrôle d'adresse a lieu à la connexion, sur
// l'IP réellement contactée : un nom qui se résout vers le réseau interne (y compris après
// redirection ou rebinding DNS) est refusé. allowPrivate ne sert qu'aux tests.
func newClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || (!allowPrivate && !publicAddr(ap.Addr())) {
				return errForbiddenAddress
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy: nil, // un mandataire contournerait le contrôle d'adresse
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   requestTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("trop de redirections")
			}
			if req.URL.Scheme != "https" {
				return errForbiddenAddress
			}
			return nil
		},
	}
}
