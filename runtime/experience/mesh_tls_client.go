package main

import (
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
)

// circleTLSClientConfig builds TLS 1.3 client settings for circle device PKI.
// Public CA and hostname verification are skipped; VerifyPeerCertificate validates ed25519 device certs.
func circleTLSClientConfig(cert tls.Certificate, serverName string, verifyKey func(ed25519.PublicKey) error) *tls.Config {
	return &tls.Config{ // NOSONAR go:S4830 — ed25519 circle PKI in VerifyPeerCertificate; public CA skipped intentionally
		MinVersion:            tls.VersionTLS13,
		Certificates:          []tls.Certificate{cert},
		ServerName:            serverName,
		InsecureSkipVerify:    true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			return verifyDeviceCert(raw, verifyKey)
		},
	}
}
