package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

// tlsConfig builds a *tls.Config from opts, or returns nil when TLS is off.
// Explicit cert+key files win; otherwise --tls generates a self-signed cert
// covering localhost and this host's name/IPs (enough to get a secure context
// so browser features like Document Picture-in-Picture work over the LAN).
func tlsConfig(opts *Options) (*tls.Config, error) {
	if opts.TLSCert != "" && opts.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(opts.TLSCert, opts.TLSKey)
		if err != nil {
			return nil, fmt.Errorf("load tls keypair: %w", err)
		}
		return newTLSConfig(cert), nil
	}
	if !opts.TLSAuto {
		return nil, nil
	}
	cert, err := selfSignedCert()
	if err != nil {
		return nil, fmt.Errorf("generate self-signed cert: %w", err)
	}
	return newTLSConfig(cert), nil
}

// newTLSConfig pins HTTP/1.1: gorilla/websocket does not support HTTP/2, and
// auto-negotiated h2 breaks the terminal WebSocket bridge.
func newTLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
	}
}

// selfSignedCert mints an in-memory ECDSA cert valid for ~1 year, with SANs for
// localhost, loopback, this host's name, and its non-loopback IPs.
// ponytail: self-signed, so browsers warn once then trust for the session.
// Upgrade path: pass --tls-cert/--tls-key with a real cert (e.g. mkcert, ACME).
func selfSignedCert() (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	dnsNames := []string{"localhost"}
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	if host, err := os.Hostname(); err == nil && host != "" {
		dnsNames = append(dnsNames, host)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				ips = append(ips, ipnet.IP)
			}
		}
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "termyard"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}
