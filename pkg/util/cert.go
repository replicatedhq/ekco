package util

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"fmt"

	kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
)

// GetEncryptionAlgorithmType returns the encryption algorithm type for a given certificate
// https://kubernetes.io/docs/reference/config-api/kubeadm-config.v1beta4/#kubeadm-k8s-io-v1beta4-ClusterConfiguration
// Can be one of "RSA-2048" (default), "RSA-3072", "RSA-4096" or "ECDSA-P256"
func GetEncryptionAlgorithmType(cert *x509.Certificate) (kubeadmapi.EncryptionAlgorithmType, error) {
	switch pubKey := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		keySize := pubKey.Size() * 8 // Size() returns bytes, convert to bits
		switch keySize {
		case 2048:
			return kubeadmapi.EncryptionAlgorithmRSA2048, nil
		case 3072:
			return kubeadmapi.EncryptionAlgorithmRSA3072, nil
		case 4096:
			return kubeadmapi.EncryptionAlgorithmRSA4096, nil
		default:
			return "", fmt.Errorf("unsupported RSA key size: %d bits", keySize)
		}
	case *ecdsa.PublicKey:
		if pubKey.Curve == elliptic.P256() {
			return kubeadmapi.EncryptionAlgorithmECDSAP256, nil
		}
		return "", fmt.Errorf("unsupported ECDSA curve: %s", pubKey.Curve.Params().Name)
	default:
		return "", fmt.Errorf("unsupported public key type: %T", pubKey)
	}
}
