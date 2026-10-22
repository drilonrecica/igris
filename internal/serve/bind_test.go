package serve

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExposureOf(t *testing.T) {
	for addr, want := range map[string]Exposure{
		"127.0.0.1":           Loopback,
		"127.9.9.9":           Loopback,
		"::1":                 Loopback,
		"::ffff:127.0.0.1":    Loopback,
		"100.64.0.1":          Tailscale,
		"100.101.102.103":     Tailscale,
		"100.127.255.255":     Tailscale,
		"fd7a:115c:a1e0::1":   Tailscale,
		"fd7a:115c:a1e0:ab::": Tailscale,
		"100.63.255.255":      Public,
		"100.128.0.0":         Public,
		"fd7a:115c:a1e1::1":   Public,
		"192.168.1.10":        Public,
		"10.0.0.1":            Public,
		"0.0.0.0":             Public,
		"::":                  Public,
		"8.8.8.8":             Public,
	} {
		if got := ExposureOf(netip.MustParseAddr(addr)); got != want {
			t.Errorf("ExposureOf(%s) = %s, want %s", addr, got, want)
		}
	}
}

// The bind rules (SPEC §14): loopback and Tailscale bind as they are,
// anything else needs --listen-public and TLS, and nothing is bound when
// a rule says no.
func TestListenHTTPRules(t *testing.T) {
	cert, key := writeCert(t)
	for _, tc := range []struct {
		name string
		c    HTTPConfig
		want string
	}{
		{"public without the flag", HTTPConfig{Addr: "0.0.0.0:0"}, "pass --listen-public with [serve] tls_cert and tls_key"},
		{"public, flag, no TLS", HTTPConfig{Addr: "0.0.0.0:0", Public: true}, "--listen-public needs TLS"},
		{"LAN address without the flag", HTTPConfig{Addr: "192.168.1.10:7777", CertFile: cert, KeyFile: key}, "reachable beyond loopback and Tailscale"},
		{"a host name", HTTPConfig{Addr: "example.org:7777"}, "use an IP address"},
		{"no port", HTTPConfig{Addr: "127.0.0.1"}, "ADDR:PORT"},
		{"a bad certificate", HTTPConfig{Addr: "127.0.0.1:0", CertFile: key, KeyFile: key}, "doesn't load"},
		{"a missing certificate", HTTPConfig{Addr: "127.0.0.1:0", CertFile: cert + ".gone", KeyFile: key}, "doesn't load"},
	} {
		l, err := ListenHTTP(tc.c)
		if err == nil {
			_ = l.Close()
			t.Errorf("%s: bound %s", tc.name, l.Addr())
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
	l, err := ListenHTTP(HTTPConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	if l.Exposure != Loopback || l.TLS || !strings.HasPrefix(l.URL(), "http://127.0.0.1:") {
		t.Errorf("loopback listener: %s, tls %v, %s", l.Exposure, l.TLS, l.URL())
	}
	// The address in use is an error, not a second listener.
	if l2, err := ListenHTTP(HTTPConfig{Addr: l.Addr().String()}); err == nil {
		_ = l2.Close()
		t.Error("bound an address in use")
	} else if !strings.Contains(err.Error(), "can't listen on") {
		t.Errorf("address in use: %v", err)
	}
	_ = l.Close()
	pub, err := ListenHTTP(HTTPConfig{Addr: "0.0.0.0:0", Public: true, CertFile: cert, KeyFile: key})
	if err != nil {
		t.Fatalf("public with the flag and TLS: %v", err)
	}
	if pub.Exposure != Public || !pub.TLS {
		t.Errorf("public listener: %s, tls %v", pub.Exposure, pub.TLS)
	}
	_ = pub.Close()
}

// With TLS the listener speaks TLS 1.2 or later, and the cookie is
// Secure (TestHTTPPairing).
func TestListenHTTPServesTLS(t *testing.T) {
	cert, key := writeCert(t)
	l, err := ListenHTTP(HTTPConfig{Addr: "127.0.0.1:0", CertFile: cert, KeyFile: key})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(l.URL(), "https://") {
		t.Errorf("URL %s", l.URL())
	}
	srv := New(Options{Version: "1.6.0"})
	defer func() { _ = srv.Close() }()
	go func() {
		_ = srv.ServeRemote(l, srv.Remote(RemoteOptions{TLS: true, Token: func() (string, error) { return goodToken, nil }}))
	}()
	pool := x509.NewCertPool()
	pemData, _ := os.ReadFile(cert) //nolint:gosec // test temp dir
	pool.AppendCertsFromPEM(pemData)
	for _, v := range []uint16{tls.VersionTLS11, tls.VersionTLS12} {
		c := &http.Client{Timeout: patience, Transport: &http.Transport{TLSClientConfig: &tls.Config{ //nolint:gosec // checks that old versions are refused
			RootCAs: pool, MinVersion: tls.VersionTLS10, MaxVersion: v,
		}}}
		req, _ := http.NewRequest("GET", l.URL()+"/", nil)
		req.Header.Set("Authorization", "Bearer "+goodToken)
		resp, err := c.Do(req)
		if v == tls.VersionTLS11 {
			if err == nil {
				_ = resp.Body.Close()
				t.Error("TLS 1.1 was accepted")
			}
			continue
		}
		if err != nil {
			t.Fatalf("TLS 1.2: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Strict-Transport-Security") == "" {
			t.Errorf("over TLS: %d, HSTS %q", resp.StatusCode, resp.Header.Get("Strict-Transport-Security"))
		}
	}
}

// writeCert writes a self-signed certificate for 127.0.0.1 and its key.
func writeCert(t *testing.T) (cert, key string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "igris test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cert, key = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}
	return cert, key
}
