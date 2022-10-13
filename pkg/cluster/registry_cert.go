package cluster

import (
	"context"
	"crypto"
	cryptorand "crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/client-go/util/cert"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/keyutil"
	kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	certsphase "k8s.io/kubernetes/cmd/kubeadm/app/phases/certs"
	"k8s.io/kubernetes/cmd/kubeadm/app/util/pkiutil"
)

type certConfig struct {
	certutil.Config
	NotBefore          *time.Time
	NotAfter           *time.Time
	PublicKeyAlgorithm x509.PublicKeyAlgorithm
}

func (c *Controller) RotateRegistryCert() error {
	ns := c.Config.RegistryCertNamespace
	name := c.Config.RegistryCertSecret

	if ns == "" || name == "" {
		c.Log.Debugf("Registry namespace and secret not set, skipping certificate renewal")
		return nil
	}

	cert, err := c.readRegistryCert(ns, name)
	if err != nil {
		return errors.Wrapf(err, "load existing certificate")
	}
	if cert == nil {
		c.Log.Debugf("Registry cert not found")
		return nil
	}

	ttl := time.Until(cert.NotAfter)
	if ttl > c.Config.RotateCertsTTL {
		c.Log.Debugf("Registry cert has %s until expiration, skipping renewal", duration.ShortHumanDuration(ttl))
		return nil
	}

	caCert, caKey, err := certsphase.LoadCertificateAuthority("/etc/kubernetes/pki", "ca")
	if err != nil {
		return errors.Wrap(err, "load cluster CA")
	}

	config := certToConfig(cert)

	newCert, newKey, err := generateNewCertAndKey(caCert, caKey, config)
	if err != nil {
		return errors.Wrap(err, "generate new cert")
	}

	if err := c.updateRegistryCert(ns, name, newCert, newKey); err != nil {
		return errors.Wrap(err, "save new cert")
	}

	c.Log.Infof("Renewed registry cert")

	selector := metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(labels.Set{"app": "registry"}).String(),
	}
	if err := c.Config.Client.CoreV1().Pods(ns).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, selector); err != nil {
		return errors.Wrap(err, "restart registry")
	}

	c.Log.Infof("Restarted registry pods")

	return nil
}

func (c *Controller) readRegistryCert(namespace, name string) (*x509.Certificate, error) {
	secret, err := c.Config.Client.CoreV1().Secrets(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			return nil, nil
		}
		return nil, errors.Wrapf(err, "get secret %s/%s", namespace, name)
	}

	certs, err := cert.ParseCertsPEM(secret.Data["registry.crt"])
	if err != nil {
		return nil, errors.Wrapf(err, "parse registry.crt")
	}
	if len(certs) != 1 {
		return nil, fmt.Errorf("expected exactly 1 cert in registry.crt, got %d", len(certs))
	}

	return certs[0], nil
}

func (c *Controller) updateRegistryCert(namespace, name string, crt *x509.Certificate, key crypto.PrivateKey) error {
	secret, err := c.Config.Client.CoreV1().Secrets(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "get secret %s/%s", namespace, name)
	}

	crtData, err := cert.EncodeCertificates(crt)
	if err != nil {
		return errors.Wrapf(err, "encode certificate")
	}
	secret.Data["registry.crt"] = crtData

	keyData, err := keyutil.MarshalPrivateKeyToPEM(key)
	if err != nil {
		return errors.Wrapf(err, "encode key")
	}
	secret.Data["registry.key"] = keyData

	if _, err := c.Config.Client.CoreV1().Secrets(namespace).Update(context.TODO(), secret, metav1.UpdateOptions{}); err != nil {
		return errors.Wrapf(err, "update secret %s/%s", namespace, name)
	}

	return nil
}

func certToConfig(crt *x509.Certificate) *certConfig {
	notBefore := time.Now().UTC()
	notAfter := notBefore.Add(kubeadmconstants.CertificateValidity)
	return &certConfig{
		Config: cert.Config{
			CommonName:   crt.Subject.CommonName,
			Organization: crt.Subject.Organization,
			AltNames: cert.AltNames{
				IPs:      crt.IPAddresses,
				DNSNames: crt.DNSNames,
			},
			Usages: crt.ExtKeyUsage,
		},
		NotBefore:          &notBefore,
		NotAfter:           &notAfter,
		PublicKeyAlgorithm: crt.PublicKeyAlgorithm,
	}
}

// Copied from pkiutil.NewCertAndKey
// This uses a slightly different implementation for NewSignedCert.
// The notBefore date is Now() instead of caCert's notBefore.
func generateNewCertAndKey(caCert *x509.Certificate, caKey crypto.Signer, config *certConfig) (*x509.Certificate, crypto.Signer, error) {
	if len(config.Usages) == 0 {
		return nil, nil, errors.New("must specify at least one ExtKeyUsage")
	}

	key, err := pkiutil.NewPrivateKey(config.PublicKeyAlgorithm)
	if err != nil {
		return nil, nil, errors.Wrap(err, "unable to create private key")
	}

	cert, err := newSignedCert(config, key, caCert, caKey, false)
	if err != nil {
		return nil, nil, errors.Wrap(err, "unable to sign certificate")
	}

	return cert, key, nil
}

func newSignedCert(cfg *certConfig, key crypto.Signer, caCert *x509.Certificate, caKey crypto.Signer, isCA bool) (*x509.Certificate, error) {
	serial, err := cryptorand.Int(cryptorand.Reader, new(big.Int).SetInt64(math.MaxInt64))
	if err != nil {
		return nil, err
	}
	if len(cfg.CommonName) == 0 {
		return nil, errors.New("must specify a CommonName")
	}

	keyUsage := x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature
	if isCA {
		keyUsage |= x509.KeyUsageCertSign
	}

	pkiutil.RemoveDuplicateAltNames(&cfg.AltNames)

	notBefore := time.Now().UTC()
	if cfg.NotBefore != nil {
		notBefore = *cfg.NotBefore
	}

	notAfter := time.Now().Add(kubeadmconstants.CertificateValidity).UTC()
	if cfg.NotAfter != nil {
		notAfter = *cfg.NotAfter
	}

	certTmpl := x509.Certificate{
		Subject: pkix.Name{
			CommonName:   cfg.CommonName,
			Organization: cfg.Organization,
		},
		DNSNames:              cfg.AltNames.DNSNames,
		IPAddresses:           cfg.AltNames.IPs,
		SerialNumber:          serial,
		NotBefore:             notBefore, // this line is different in pkiutil.NewSignedCert
		NotAfter:              notAfter,
		KeyUsage:              keyUsage,
		ExtKeyUsage:           cfg.Usages,
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	certDERBytes, err := x509.CreateCertificate(cryptorand.Reader, &certTmpl, caCert, key.Public(), caKey)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(certDERBytes)
}
