package cluster

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/duration"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/utils/ptr"
)

const (
	KotsadmRqliteHAReplicaCount = 3
)

func (c *Controller) RotateKurlProxyCert(ctx context.Context) error {
	ns := c.Config.KurlProxyCertNamespace
	secretName := c.Config.KurlProxyCertSecret

	// 1. Parse current cert from secret
	secret, err := c.Config.Client.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			c.Log.Debugf("Kurl proxy cert secret does not exist, skipping renewal")
			return nil
		}
		return errors.Wrapf(err, "get kurl proxy secret")
	}
	certs, err := certutil.ParseCertsPEM(secret.Data["tls.crt"])
	if err != nil {
		return errors.Wrapf(err, "parse kurl proxy secret tls.crt")
	}
	if len(certs) == 0 {
		return fmt.Errorf("no certs found in kurl proxy secret tls.crt")
	}
	cert := certs[0]

	// 2. Abort if current cert is not expiring within TTL deadline
	ttl := time.Until(cert.NotAfter)
	if ttl > c.Config.RotateCertsTTL {
		c.Log.Debugf("Kurl proxy cert has %s until expiration, skipping renewal", duration.ShortHumanDuration(ttl))
		return nil
	}

	// 3. Abort if current cert is a custom uploaded cert
	// If generated by kurl installer Subject will be "kotsadm.default.svc.cluster.local" and Issuer
	// will be empty. If already rotated by ekco then Subject will be like
	// "kotsadm.default.svc.cluster.local@1604697213" and Issuer like
	// "kotsadm.default.svc.cluster.local-ca@1604697213"
	if cert.Issuer.CommonName != "" && !strings.HasPrefix(cert.Issuer.CommonName, "kotsadm.default.svc.cluster.local") {
		c.Log.Debugf("Custom cert issuer detected in kurl proxy secret tls.crt, skipping renewal")
		return nil
	}
	if !strings.HasPrefix(cert.Subject.CommonName, "kotsadm.default.svc.cluster.local") {
		c.Log.Debugf("Custom cert subject detected in kurl proxy secret tls.crt, skipping renewal")
		return nil
	}

	// 4. Generate a new self-signed cert
	c.Log.Infof("Kurl proxy cert has %s until expiration, renewing", duration.ShortHumanDuration(ttl))
	certData, keyData, err := certutil.GenerateSelfSignedCertKey("kotsadm.default.svc.cluster.local", cert.IPAddresses, cert.DNSNames)
	if err != nil {
		return errors.Wrapf(err, "generate self-signed cert")
	}

	// 5. Update the secret
	secret.Data["tls.crt"] = certData
	secret.Data["tls.key"] = keyData
	if _, err := c.Config.Client.CoreV1().Secrets(ns).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return errors.Wrapf(err, "update")
	}

	return nil
}

// Copies apiserver-kubelet-client.crt into secret used by kotsadm to collect metrics
func (c *Controller) UpdateKubeletClientCertSecret(ctx context.Context) error {
	ns := c.Config.KotsadmKubeletCertNamespace
	secretName := c.Config.KotsadmKubeletCertSecret

	secret, err := c.Config.Client.CoreV1().Secrets(ns).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			c.Log.Debugf("Kubelet client secret does not exist, skipping update")
			return nil
		}
		return errors.Wrapf(err, "get kubelet client secret")
	}

	certData, err := os.ReadFile("/etc/kubernetes/pki/apiserver-kubelet-client.crt")
	if err != nil {
		return errors.Wrapf(err, "read /etc/kubernetes/pki/apiserver-kubelet-client.crt")
	}
	keyData, err := os.ReadFile("/etc/kubernetes/pki/apiserver-kubelet-client.key")
	if err != nil {
		return errors.Wrapf(err, "read /etc/kubernetes/pki/apiserver-kubelet-client.key")
	}

	needsUpdate := false
	if !bytes.Equal(secret.Data["client.crt"], certData) {
		needsUpdate = true
		secret.Data["client.crt"] = certData
	}
	if !bytes.Equal(secret.Data["client.key"], keyData) {
		needsUpdate = true
		secret.Data["client.key"] = keyData
	}

	if !needsUpdate {
		c.Log.Debug("Kubelet client cert has not changed, no update needed")
		return nil
	}

	c.Log.Info("Updating kubelet client cert")
	if _, err := c.Config.Client.CoreV1().Secrets(ns).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		return errors.Wrapf(err, "update")
	}

	return nil
}

func (c *Controller) EnableHAKotsadm(ctx context.Context, ns string) error {
	// kotsadm api does not currently support running as multiple replicas, so we only scale kotsadm-rqlite (the db).
	// IMPORTANT: once kotsadm api supports running as multiple replicas and this is updated to scale kotsadm, make sure
	// to NOT scale older kotsadm versions that do not support multiple replicas.
	if err := c.ScaleKotsadmRqlite(ctx, ns, KotsadmRqliteHAReplicaCount); err != nil {
		return errors.Wrap(err, "failed to scale up kotsadm-rqlite")
	}
	return nil
}

func (c *Controller) ScaleKotsadmRqlite(ctx context.Context, ns string, desiredScale int32) error {
	kotsadmRqliteSts, err := c.Config.Client.AppsV1().StatefulSets(ns).Get(ctx, "kotsadm-rqlite", metav1.GetOptions{})
	if err != nil {
		if util.IsNotFoundErr(err) {
			// could be an older kotsadm version that doesn't use rqlite, or that the kotsadm-rqlite sts simply doesn't exist,
			// in either case, it's not EKCO's problem at this point, so no-op here.
			return nil
		}
		return errors.Wrap(err, "get kotsadm-rqlite statefulset")
	}

	var currentScale int32
	if kotsadmRqliteSts.Spec.Replicas != nil {
		currentScale = *kotsadmRqliteSts.Spec.Replicas
	}
	if currentScale == desiredScale {
		return nil // already scaled
	}

	c.Log.Infof("Scaling kotsadm-rqlite Statefulset to %d replicas", desiredScale)

	kotsadmRqliteSts.Spec.Replicas = ptr.To(desiredScale)

	for i, arg := range kotsadmRqliteSts.Spec.Template.Spec.Containers[0].Args {
		if strings.HasPrefix(arg, "-bootstrap-expect") {
			kotsadmRqliteSts.Spec.Template.Spec.Containers[0].Args[i] = fmt.Sprintf("-bootstrap-expect=%d", desiredScale)
			break
		}
	}

	if _, err := c.Config.Client.AppsV1().StatefulSets(ns).Update(ctx, kotsadmRqliteSts, metav1.UpdateOptions{}); err != nil {
		return errors.Wrap(err, "update kotsadm-rqlite statefulset")
	}

	c.Log.Infof("Scaled kotsadm-rqlite Statefulset to %d replicas", desiredScale)
	return nil
}
