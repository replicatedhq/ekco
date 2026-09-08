package ekcoops

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"net"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

// generateCSR creates a PEM-encoded x509 Certificate Signing Request for use
// in tests. The caller owns the subject and SANs; the generated private key
// is discarded because the approver only inspects the request.
func generateCSR(t *testing.T, org []string, commonName string, dnsNames []string, ipAddresses []net.IP, extraEmails []string, extraURIs []string) []byte {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	var uris []*url.URL
	for _, u := range extraURIs {
		parsed, err := url.Parse(u)
		require.NoError(t, err)
		uris = append(uris, parsed)
	}

	template := x509.CertificateRequest{
		Subject: pkix.Name{
			Organization: org,
			CommonName:   commonName,
		},
		DNSNames:       dnsNames,
		IPAddresses:    ipAddresses,
		EmailAddresses: extraEmails,
		URIs:           uris,
	}

	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, &template, key)
	require.NoError(t, err)

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrBytes})
}

func TestReconcileCertificateSigningRequests(t *testing.T) {
	validNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1"},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeHostName, Address: "worker-1"},
				{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
			},
		},
	}

	apiServerDNSNames := []string{
		"kubernetes",
		"kubernetes.default",
		"kubernetes.default.svc",
		"kubernetes.default.svc.cluster.local",
		"apiserver.internal",
	}
	apiServerIPs := []net.IP{
		net.ParseIP("10.96.0.1"),
		net.ParseIP("10.128.0.7"),
	}

	validCSR := &certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1-serving"},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Username:   "system:node:worker-1",
			SignerName: "kubernetes.io/kubelet-serving",
			Usages: []certificatesv1.KeyUsage{
				certificatesv1.UsageDigitalSignature,
				certificatesv1.UsageKeyEncipherment,
				certificatesv1.UsageServerAuth,
			},
			Request: generateCSR(t, []string{"system:nodes"}, "system:node:worker-1", []string{"worker-1"}, []net.IP{net.ParseIP("10.0.0.1")}, nil, nil),
		},
	}

	tests := []struct {
		name         string
		csr          *certificatesv1.CertificateSigningRequest
		nodes        []runtime.Object
		wantApproved bool
	}{
		{
			name:         "valid kubelet-serving CSR from the node itself is approved",
			csr:          validCSR,
			nodes:        []runtime.Object{validNode},
			wantApproved: true,
		},
		{
			name: "ServiceAccount requesting API server names for a node is rejected",
			csr: &certificatesv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "attack-apiserver-names"},
				Spec: certificatesv1.CertificateSigningRequestSpec{
					Username:   "system:serviceaccount:default:pwn",
					SignerName: "kubernetes.io/kubelet-serving",
					Usages: []certificatesv1.KeyUsage{
						certificatesv1.UsageDigitalSignature,
						certificatesv1.UsageKeyEncipherment,
						certificatesv1.UsageServerAuth,
					},
					Request: generateCSR(t, []string{"system:nodes"}, "system:node:worker-1", apiServerDNSNames, apiServerIPs, nil, nil),
				},
			},
			nodes:        []runtime.Object{validNode},
			wantApproved: false,
		},
		{
			name: "bootstrap token requesting kubelet-serving cert is rejected",
			csr: &certificatesv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "attack-bootstrap"},
				Spec: certificatesv1.CertificateSigningRequestSpec{
					Username:   "system:bootstrap:token",
					SignerName: "kubernetes.io/kubelet-serving",
					Usages: []certificatesv1.KeyUsage{
						certificatesv1.UsageDigitalSignature,
						certificatesv1.UsageKeyEncipherment,
						certificatesv1.UsageServerAuth,
					},
					Request: generateCSR(t, []string{"system:nodes"}, "system:node:worker-1", []string{"worker-1"}, []net.IP{net.ParseIP("10.0.0.1")}, nil, nil),
				},
			},
			nodes:        []runtime.Object{validNode},
			wantApproved: false,
		},
		{
			name: "CSR with wrong subject organization is rejected",
			csr: &certificatesv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "wrong-organization"},
				Spec: certificatesv1.CertificateSigningRequestSpec{
					Username:   "system:node:worker-1",
					SignerName: "kubernetes.io/kubelet-serving",
					Usages: []certificatesv1.KeyUsage{
						certificatesv1.UsageDigitalSignature,
						certificatesv1.UsageKeyEncipherment,
						certificatesv1.UsageServerAuth,
					},
					Request: generateCSR(t, []string{"system:foo"}, "system:node:worker-1", []string{"worker-1"}, []net.IP{net.ParseIP("10.0.0.1")}, nil, nil),
				},
			},
			nodes:        []runtime.Object{validNode},
			wantApproved: false,
		},
		{
			name: "CSR with requester name not matching CommonName is rejected",
			csr: &certificatesv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "mismatched-requester"},
				Spec: certificatesv1.CertificateSigningRequestSpec{
					Username:   "system:node:worker-2",
					SignerName: "kubernetes.io/kubelet-serving",
					Usages: []certificatesv1.KeyUsage{
						certificatesv1.UsageDigitalSignature,
						certificatesv1.UsageKeyEncipherment,
						certificatesv1.UsageServerAuth,
					},
					Request: generateCSR(t, []string{"system:nodes"}, "system:node:worker-1", []string{"worker-1"}, []net.IP{net.ParseIP("10.0.0.1")}, nil, nil),
				},
			},
			nodes:        []runtime.Object{validNode},
			wantApproved: false,
		},
		{
			name: "CSR with SAN not belonging to the node is rejected",
			csr: &certificatesv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "san-not-on-node"},
				Spec: certificatesv1.CertificateSigningRequestSpec{
					Username:   "system:node:worker-1",
					SignerName: "kubernetes.io/kubelet-serving",
					Usages: []certificatesv1.KeyUsage{
						certificatesv1.UsageDigitalSignature,
						certificatesv1.UsageKeyEncipherment,
						certificatesv1.UsageServerAuth,
					},
					Request: generateCSR(t, []string{"system:nodes"}, "system:node:worker-1", apiServerDNSNames, apiServerIPs, nil, nil),
				},
			},
			nodes:        []runtime.Object{validNode},
			wantApproved: false,
		},
		{
			name: "CSR for a non-existent node is rejected",
			csr: &certificatesv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "missing-node"},
				Spec: certificatesv1.CertificateSigningRequestSpec{
					Username:   "system:node:ghost",
					SignerName: "kubernetes.io/kubelet-serving",
					Usages: []certificatesv1.KeyUsage{
						certificatesv1.UsageDigitalSignature,
						certificatesv1.UsageKeyEncipherment,
						certificatesv1.UsageServerAuth,
					},
					Request: generateCSR(t, []string{"system:nodes"}, "system:node:ghost", []string{"ghost"}, []net.IP{net.ParseIP("10.0.0.99")}, nil, nil),
				},
			},
			nodes:        []runtime.Object{},
			wantApproved: false,
		},
		{
			name: "CSR with EmailAddresses is rejected",
			csr: &certificatesv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "email-in-csr"},
				Spec: certificatesv1.CertificateSigningRequestSpec{
					Username:   "system:node:worker-1",
					SignerName: "kubernetes.io/kubelet-serving",
					Usages: []certificatesv1.KeyUsage{
						certificatesv1.UsageDigitalSignature,
						certificatesv1.UsageKeyEncipherment,
						certificatesv1.UsageServerAuth,
					},
					Request: generateCSR(t, []string{"system:nodes"}, "system:node:worker-1", []string{"worker-1"}, []net.IP{net.ParseIP("10.0.0.1")}, []string{"attacker@example.com"}, nil),
				},
			},
			nodes:        []runtime.Object{validNode},
			wantApproved: false,
		},
		{
			name: "CSR with URIs is rejected",
			csr: &certificatesv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "uri-in-csr"},
				Spec: certificatesv1.CertificateSigningRequestSpec{
					Username:   "system:node:worker-1",
					SignerName: "kubernetes.io/kubelet-serving",
					Usages: []certificatesv1.KeyUsage{
						certificatesv1.UsageDigitalSignature,
						certificatesv1.UsageKeyEncipherment,
						certificatesv1.UsageServerAuth,
					},
					Request: generateCSR(t, []string{"system:nodes"}, "system:node:worker-1", []string{"worker-1"}, []net.IP{net.ParseIP("10.0.0.1")}, nil, []string{"spiffe://example.com/worker-1"}),
				},
			},
			nodes:        []runtime.Object{validNode},
			wantApproved: false,
		},
		{
			name: "CSR with a different signer is not approved",
			csr: &certificatesv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "wrong-signer"},
				Spec: certificatesv1.CertificateSigningRequestSpec{
					Username:   "system:node:worker-1",
					SignerName: "kubernetes.io/kube-apiserver-client",
					Usages: []certificatesv1.KeyUsage{
						certificatesv1.UsageDigitalSignature,
						certificatesv1.UsageKeyEncipherment,
						certificatesv1.UsageServerAuth,
					},
					Request: generateCSR(t, []string{"system:nodes"}, "system:node:worker-1", []string{"worker-1"}, []net.IP{net.ParseIP("10.0.0.1")}, nil, nil),
				},
			},
			nodes:        []runtime.Object{validNode},
			wantApproved: false,
		},
		{
			name: "already-approved CSR is left alone",
			csr: &certificatesv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{Name: "already-approved"},
				Status: certificatesv1.CertificateSigningRequestStatus{
					Conditions: []certificatesv1.CertificateSigningRequestCondition{
						{
							Type:   certificatesv1.CertificateApproved,
							Status: corev1.ConditionTrue,
						},
					},
				},
				Spec: certificatesv1.CertificateSigningRequestSpec{
					Username:   "system:node:worker-1",
					SignerName: "kubernetes.io/kubelet-serving",
					Usages: []certificatesv1.KeyUsage{
						certificatesv1.UsageDigitalSignature,
						certificatesv1.UsageKeyEncipherment,
						certificatesv1.UsageServerAuth,
					},
					Request: validCSR.Spec.Request,
				},
			},
			nodes:        []runtime.Object{validNode},
			wantApproved: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := append([]runtime.Object{tt.csr}, tt.nodes...)
			client := fake.NewSimpleClientset(resources...)
			operator := New(Config{AutoApproveKubeletCertSigningRequests: true}, client, nil, zap.NewNop().Sugar())

			err := operator.reconcileCertificateSigningRequests(context.Background())
			require.NoError(t, err)

			got, err := client.CertificatesV1().CertificateSigningRequests().Get(context.Background(), tt.csr.Name, metav1.GetOptions{})
			require.NoError(t, err)

			approved := false
			for _, cond := range got.Status.Conditions {
				if cond.Type == certificatesv1.CertificateApproved && cond.Status == corev1.ConditionTrue {
					approved = true
				}
			}
			require.Equal(t, tt.wantApproved, approved, "CSR %q approval status mismatch", tt.csr.Name)
		})
	}
}
