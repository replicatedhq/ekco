package cluster

import (
	"context"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"github.com/projectcontour/contour/pkg/certs"
	"github.com/replicatedhq/ekco/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/client-go/util/cert"
)

func (c *Controller) RotateContourCerts() error {
	contourNamespace := c.Config.ContourNamespace
	contourSecretName := c.Config.ContourCertSecret
	envoySecretName := c.Config.EnvoyCertSecret

	if contourNamespace == "" || contourSecretName == "" || envoySecretName == "" {
		c.Log.Debugf("Contour namespace and secrets not set, skipping certificates renewal")
		return nil
	}

	caCert, contourCert, envoyCert, err := c.readContourCerts(contourNamespace, contourSecretName, envoySecretName)
	if err != nil {
		return errors.Wrapf(err, "read contour certificates")
	}
	if caCert == nil {
		c.Log.Debugf("Contour ca cert not found, skipping renewal")
		return nil
	}
	if contourCert == nil {
		c.Log.Debugf("Contour cert not found, skipping renewal")
		return nil
	}
	if envoyCert == nil {
		c.Log.Debugf("Envoy cert not found, skipping renewal")
		return nil
	}

	if !c.shouldRotateContourCerts(caCert, contourCert, envoyCert) {
		c.Log.Debugf("Contour certs have more than %s until expiration, skipping renewal", duration.ShortHumanDuration(c.Config.RotateCertsTTL))
		return nil
	}

	if err := c.updateContourCerts(contourNamespace, contourSecretName, envoySecretName); err != nil {
		return errors.Wrap(err, "update certs")
	}

	c.Log.Infof("Renewed contour and envoy certs")

	// rollout restart envoy pods
	envoyPatchPayload := fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":"%s"}}}}}`, time.Now().Format(time.RFC3339))
	if _, err := c.Config.Client.AppsV1().DaemonSets(contourNamespace).Patch(context.TODO(), "envoy", k8stypes.StrategicMergePatchType, []byte(envoyPatchPayload), metav1.PatchOptions{}); err != nil {
		return errors.Wrap(err, "restart envoy")
	}

	c.Log.Infof("Restarting envoy pods")

	return nil
}

func (c *Controller) shouldRotateContourCerts(caCert, contourCert, envoyCert *x509.Certificate) bool {
	caCertTTL := time.Until(caCert.NotAfter)
	if caCertTTL <= c.Config.RotateCertsTTL {
		return true
	}
	contourCertTTL := time.Until(contourCert.NotAfter)
	if contourCertTTL <= c.Config.RotateCertsTTL {
		return true
	}
	envoyCertTTL := time.Until(envoyCert.NotAfter)
	return envoyCertTTL <= c.Config.RotateCertsTTL
}

func (c *Controller) readContourCerts(contourNamespace, contourSecretName, envoySecretName string) (*x509.Certificate, *x509.Certificate, *x509.Certificate, error) {
	contourSecret, err := c.Config.Client.CoreV1().Secrets(contourNamespace).Get(context.TODO(), contourSecretName, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, errors.Wrapf(err, "get secret %s/%s", contourNamespace, contourSecretName)
	}

	envoySecret, err := c.Config.Client.CoreV1().Secrets(contourNamespace).Get(context.TODO(), envoySecretName, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			return nil, nil, nil, nil
		}
		return nil, nil, nil, errors.Wrapf(err, "get secret %s/%s", contourNamespace, envoySecretName)
	}

	caCertKey := "ca.crt"
	certs, err := cert.ParseCertsPEM(contourSecret.Data[caCertKey])
	if err != nil {
		return nil, nil, nil, errors.Wrapf(err, "parse %s", caCertKey)
	}
	if len(certs) != 1 {
		return nil, nil, nil, fmt.Errorf("expected exactly 1 ca cert in %s, got %d", caCertKey, len(certs))
	}
	caCert := certs[0]

	certs, err = cert.ParseCertsPEM(contourSecret.Data[corev1.TLSCertKey])
	if err != nil {
		return nil, nil, nil, errors.Wrapf(err, "parse %s", corev1.TLSCertKey)
	}
	if len(certs) != 1 {
		return nil, nil, nil, fmt.Errorf("expected exactly 1 cert for contour in %s, got %d", corev1.TLSCertKey, len(certs))
	}
	contourCert := certs[0]

	certs, err = cert.ParseCertsPEM(envoySecret.Data[corev1.TLSCertKey])
	if err != nil {
		return nil, nil, nil, errors.Wrapf(err, "parse %s", corev1.TLSCertKey)
	}
	if len(certs) != 1 {
		return nil, nil, nil, fmt.Errorf("expected exactly 1 cert for envoy in %s, got %d", corev1.TLSCertKey, len(certs))
	}
	envoyCert := certs[0]

	return caCert, contourCert, envoyCert, nil
}

func (c *Controller) updateContourCerts(contourNamespace, contourSecretName, envoySecretName string) error {
	generatedCerts, err := certs.GenerateCerts(
		&certs.Configuration{
			Lifetime:  certs.DefaultCertificateLifetime,
			Namespace: contourNamespace,
		})
	if err != nil {
		return errors.Wrap(err, "failed to generate certificates")
	}

	caCertKey := "ca.crt"

	// contour cert secret
	contourSecret, err := c.Config.Client.CoreV1().Secrets(contourNamespace).Get(context.TODO(), contourSecretName, metav1.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "get secret %s/%s", contourNamespace, contourSecretName)
	}

	contourSecret.Data[caCertKey] = generatedCerts.CACertificate
	contourSecret.Data[corev1.TLSCertKey] = generatedCerts.ContourCertificate
	contourSecret.Data[corev1.TLSPrivateKeyKey] = generatedCerts.ContourPrivateKey

	if _, err := c.Config.Client.CoreV1().Secrets(contourNamespace).Update(context.TODO(), contourSecret, metav1.UpdateOptions{}); err != nil {
		return errors.Wrapf(err, "update secret %s/%s", contourNamespace, contourSecretName)
	}

	// envoy cert secret
	envoySecret, err := c.Config.Client.CoreV1().Secrets(contourNamespace).Get(context.TODO(), envoySecretName, metav1.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "get secret %s/%s", contourNamespace, envoySecretName)
	}

	envoySecret.Data[caCertKey] = generatedCerts.CACertificate
	envoySecret.Data[corev1.TLSCertKey] = generatedCerts.EnvoyCertificate
	envoySecret.Data[corev1.TLSPrivateKeyKey] = generatedCerts.EnvoyPrivateKey

	if _, err := c.Config.Client.CoreV1().Secrets(contourNamespace).Update(context.TODO(), envoySecret, metav1.UpdateOptions{}); err != nil {
		return errors.Wrapf(err, "update secret %s/%s", contourNamespace, envoySecretName)
	}

	return nil
}
