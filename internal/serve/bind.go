package serve

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/drilonrecica/igris/internal/config"
)

// Exposure says who can reach an HTTP listener's address (SPEC §14).
type Exposure int

const (
	// Loopback is this machine only; elsewhere through ssh -L.
	Loopback Exposure = iota
	// Tailscale is the tailnet's devices, over WireGuard.
	Tailscale
	// Public is anything else: a LAN address, 0.0.0.0, ::.
	Public
)

func (e Exposure) String() string {
	switch e {
	case Loopback:
		return "loopback"
	case Tailscale:
		return "Tailscale"
	}
	return "public"
}

// Tailscale's address ranges: its CGNAT IPv4 range and its ULA prefix.
var tailscaleRanges = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
}

// ExposureOf classifies addr.
func ExposureOf(addr netip.Addr) Exposure {
	addr = addr.Unmap()
	if addr.IsLoopback() {
		return Loopback
	}
	for _, p := range tailscaleRanges {
		if p.Contains(addr) {
			return Tailscale
		}
	}
	return Public
}

// HTTPConfig is what the HTTP listener binds (SPEC §14).
type HTTPConfig struct {
	// Addr is "ADDR:PORT" (config.ParseHTTPAddr).
	Addr string
	// Public is --listen-public: an address beyond loopback and
	// Tailscale may bind, with TLS.
	Public bool
	// CertFile and KeyFile are the PEM files TLS is served from, both or
	// neither; resolved by the caller.
	CertFile, KeyFile string
}

// HTTPListener is a bound HTTP listener.
type HTTPListener struct {
	net.Listener
	// Exposure is who can reach it; TLS whether it serves TLS.
	Exposure Exposure
	TLS      bool
}

// URL is the listener's base URL, e.g. "https://100.64.0.1:7777".
func (l *HTTPListener) URL() string {
	scheme := "http"
	if l.TLS {
		scheme = "https"
	}
	return scheme + "://" + l.Addr().String()
}

// ListenHTTP checks c against the bind rules and binds it: loopback and
// Tailscale addresses as they are, any other only with Public and TLS.
// The error says what to set.
func ListenHTTP(c HTTPConfig) (*HTTPListener, error) {
	// Port 0 (any free port) is for tests: the config and --http refuse it.
	addr, anyPort := strings.CutSuffix(c.Addr, ":0")
	if anyPort {
		addr += ":1"
	}
	ap, err := config.ParseHTTPAddr(addr)
	if err != nil {
		return nil, fmt.Errorf("the HTTP address %s: %w", c.Addr, err)
	}
	if anyPort {
		ap = netip.AddrPortFrom(ap.Addr(), 0)
	}
	tlsOn := c.CertFile != "" && c.KeyFile != ""
	exp := ExposureOf(ap.Addr())
	if exp == Public {
		switch {
		case !c.Public:
			return nil, fmt.Errorf("%s is reachable beyond loopback and Tailscale; use a loopback address (with ssh -L) or this machine's Tailscale address, or pass --listen-public with [serve] tls_cert and tls_key", ap)
		case !tlsOn:
			return nil, fmt.Errorf("--listen-public needs TLS: set [serve] tls_cert and tls_key in igris.toml")
		}
	}
	var cfg *tls.Config
	if tlsOn {
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("the TLS certificate (serve.tls_cert, serve.tls_key) doesn't load: %w", err)
		}
		cfg = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	}
	l, err := net.Listen("tcp", ap.String())
	if err != nil {
		var oe *net.OpError
		if errors.As(err, &oe) && oe.Err != nil {
			err = oe.Err
		}
		return nil, fmt.Errorf("can't listen on %s: %w", ap, err)
	}
	hl := &HTTPListener{Listener: l, Exposure: exp, TLS: tlsOn}
	if cfg != nil {
		hl.Listener = tls.NewListener(l, cfg)
	}
	return hl, nil
}
