package cluster

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func Test_generateNewCertAndKey(t *testing.T) {
	caNotBefore := time.Now()
	caNotAfter := caNotBefore.AddDate(10, 0, 0)
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(2019),
		Subject: pkix.Name{
			CommonName:    "EKCO Company",
			Organization:  []string{"Company, INC."},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{"San Francisco"},
			StreetAddress: []string{"Golden Gate Bridge"},
			PostalCode:    []string{"94016"},
		},
		NotBefore:             caNotBefore,
		NotAfter:              caNotAfter,
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv4(1, 2, 3, 4)},
		DNSNames:              []string{"ekco-1.local", "ekco-1.local"},
		PublicKeyAlgorithm:    x509.RSA,
	}
	tests := []struct {
		name string
		ca   *x509.Certificate
	}{
		{
			name: "renew cert",
			ca:   ca,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caKey, err := rsa.GenerateKey(rand.Reader, 2048)
			assert.NoError(t, err)

			caBytes, err := x509.CreateCertificate(rand.Reader, test.ca, test.ca, &caKey.PublicKey, caKey)
			assert.NoError(t, err)

			parsedCA, err := x509.ParseCertificate(caBytes)
			assert.NoError(t, err)

			cfg, err := certToConfig(parsedCA)
			assert.NoError(t, err)
			newCert, _, err := generateNewCertAndKey(parsedCA, caKey, cfg)
			assert.NoError(t, err)

			// assert.Equal(t, cfg.AltNames.IPs, newCert.IPAddresses) // TODO: compare ipv4 and ipv6
			assert.Equal(t, cfg.AltNames.DNSNames, newCert.DNSNames)

			roots := x509.NewCertPool()
			roots.AddCert(parsedCA)

			verifyPassOpts := x509.VerifyOptions{
				Roots:         roots,
				DNSName:       "ekco-1.local",
				Intermediates: x509.NewCertPool(),
			}

			_, err = newCert.Verify(verifyPassOpts)
			assert.NoError(t, err)

			verifyPassOpts = x509.VerifyOptions{
				Roots:         roots,
				DNSName:       "ekco-1.local",
				Intermediates: x509.NewCertPool(),
				CurrentTime:   time.Now().Add(time.Hour * 24 * 300), // we are now close to a year in the future
			}

			_, err = newCert.Verify(verifyPassOpts)
			assert.NoError(t, err)

			verifyFailOpts := x509.VerifyOptions{
				Roots:         roots,
				DNSName:       "ekco-1.local",
				Intermediates: x509.NewCertPool(),
				CurrentTime:   time.Now().Add(time.Hour * 24 * 375), // we are now more than a year in the future
			}

			_, err = newCert.Verify(verifyFailOpts)
			assert.Error(t, err)

			verifyFailOpts = x509.VerifyOptions{
				Roots:         roots,
				DNSName:       "ekco-1.local",
				Intermediates: x509.NewCertPool(),
				CurrentTime:   time.Now().Add(time.Hour * -1), // we are now shortly before the cert was created
			}

			_, err = newCert.Verify(verifyFailOpts)
			assert.Error(t, err)
		})
	}
}
