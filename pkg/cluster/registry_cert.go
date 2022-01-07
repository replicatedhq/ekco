package cluster

import (
	"context"
	"crypto"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/pkg/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/keyutil"
	certsphase "k8s.io/kubernetes/cmd/kubeadm/app/phases/certs"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/certs/renewal"
	"k8s.io/kubernetes/cmd/kubeadm/app/util/pkiutil"
)

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

	newCert, newKey, err := renewal.NewFileRenewer(caCert, caKey).Renew(config)
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
		if k8serrors.IsNotFound(err) {
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

func certToConfig(crt *x509.Certificate) *pkiutil.CertConfig {
	return &pkiutil.CertConfig{
		Config: cert.Config{
			CommonName:   crt.Subject.CommonName,
			Organization: crt.Subject.Organization,
			AltNames: cert.AltNames{
				IPs:      crt.IPAddresses,
				DNSNames: crt.DNSNames,
			},
			Usages: crt.ExtKeyUsage,
		},
		NotAfter:           &crt.NotAfter,
		PublicKeyAlgorithm: crt.PublicKeyAlgorithm,
	}
}
