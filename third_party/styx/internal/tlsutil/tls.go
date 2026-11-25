// Package tlsutil provides TLS configuration utilities.
package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	configv1 "gitee.com/linpbin/styx/api/config/v1"
)

// LoadServerTLSConfig loads TLS configuration for server.
// cert_file and key_file are required.
// ca_file is optional for mutual TLS (client certificate verification).
func LoadServerTLSConfig(cfg *configv1.TLSConfig) (*tls.Config, error) {
	if cfg.GetCertFile() == "" || cfg.GetKeyFile() == "" {
		return nil, fmt.Errorf("cert_file and key_file are required")
	}

	// Load server certificate
	cert, err := tls.LoadX509KeyPair(cfg.GetCertFile(), cfg.GetKeyFile())
	if err != nil {
		return nil, fmt.Errorf("failed to load certificate: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// Load CA certificate for mutual TLS (optional)
	if cfg.GetCaFile() != "" {
		caCert, err := os.ReadFile(cfg.GetCaFile())
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		tlsConfig.ClientCAs = caCertPool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsConfig, nil
}

// LoadClientTLSConfig loads TLS configuration for client.
// cert_file and key_file are optional (for mutual TLS).
// ca_file is optional (for server certificate verification).
func LoadClientTLSConfig(cfg *configv1.TLSConfig) (*tls.Config, error) {
	if cfg == nil {
		return nil, fmt.Errorf("TLS config is required")
	}
	if (cfg.GetCertFile() == "") != (cfg.GetKeyFile() == "") {
		return nil, fmt.Errorf("cert_file and key_file must be configured together")
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	// Load client certificate (for mutual TLS)
	if cfg.GetCertFile() != "" {
		cert, err := tls.LoadX509KeyPair(cfg.GetCertFile(), cfg.GetKeyFile())
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Load CA certificate to verify server
	if cfg.GetCaFile() != "" {
		caCert, err := os.ReadFile(cfg.GetCaFile())
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, nil
}
