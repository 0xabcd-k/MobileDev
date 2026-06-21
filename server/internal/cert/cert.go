package cert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const validity = 365 * 24 * time.Hour

func Ensure(certFile, keyFile string) (tls.Certificate, bool, error) {
	if filesExist(certFile, keyFile) {
		certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
		return certificate, false, err
	}

	if fileExists(certFile) != fileExists(keyFile) {
		return tls.Certificate{}, false, errors.New("only one TLS certificate file exists; remove both cert files or restore the missing file")
	}

	if err := os.MkdirAll(filepath.Dir(certFile), 0o755); err != nil {
		return tls.Certificate{}, false, err
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o755); err != nil {
		return tls.Certificate{}, false, err
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, false, err
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, false, err
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"MobileDev"},
			CommonName:   "MobileDev Server",
		},
		NotBefore:             now,
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("::1"),
		},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, false, err
	}

	certOut, err := os.OpenFile(certFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return tls.Certificate{}, false, err
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		_ = certOut.Close()
		return tls.Certificate{}, false, err
	}
	if err := certOut.Close(); err != nil {
		return tls.Certificate{}, false, err
	}

	keyOut, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return tls.Certificate{}, false, err
	}
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: privateKeyBytes}); err != nil {
		_ = keyOut.Close()
		return tls.Certificate{}, false, err
	}
	if err := keyOut.Close(); err != nil {
		return tls.Certificate{}, false, err
	}

	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	return certificate, true, err
}

func filesExist(paths ...string) bool {
	for _, path := range paths {
		if !fileExists(path) {
			return false
		}
	}
	return true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
