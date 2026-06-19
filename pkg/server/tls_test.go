package server

import (
	"crypto/x509"
	"testing"
)

func TestTLSConfigOff(t *testing.T) {
	cfg, err := tlsConfig(&Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatal("expected nil config when TLS disabled")
	}
}

func TestTLSConfigAuto(t *testing.T) {
	cfg, err := tlsConfig(&Options{TLSAuto: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil || len(cfg.Certificates) != 1 {
		t.Fatal("expected one self-signed cert")
	}
	// HTTP/1.1 must be pinned: gorilla/websocket breaks over h2.
	if len(cfg.NextProtos) != 1 || cfg.NextProtos[0] != "http/1.1" {
		t.Fatalf("expected NextProtos [http/1.1], got %v", cfg.NextProtos)
	}
	// Cert must parse and carry SANs (localhost at minimum).
	leaf, err := x509.ParseCertificate(cfg.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	found := false
	for _, n := range leaf.DNSNames {
		if n == "localhost" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected localhost in SANs, got %v", leaf.DNSNames)
	}
}
