package objectstore

import (
	"context"
	"github.com/replicatedhq/ekco/pkg/util"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"testing"
)

func Test_updateKotsadmObjectStore(t *testing.T) {
	tests := []struct {
		name      string
		client    kubernetes.Interface
		accessKey string
		secretKey string
		hostname  string
		endpoint  string
		wantErr   bool
		validate  func(t *testing.T, client kubernetes.Interface)
	}{
		{
			name: "no kotsadm, no update",
			client: fake.NewSimpleClientset(
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
				},
			),
			accessKey: "accessKey",
			secretKey: "secretKey",
			hostname:  "hostname",
			endpoint:  "endpoint",
			wantErr:   false,
			validate: func(t *testing.T, client kubernetes.Interface) {
				_, err := client.CoreV1().Secrets("default").Get(context.Background(), "kotsadm-s3", metav1.GetOptions{})
				require.Error(t, err)
				if !util.IsNotFoundErr(err) {
					t.Fatalf("expected not found error, got %s", err.Error())
				}

				return
			},
		},
		{
			name: "minio to ekco",
			client: fake.NewSimpleClientset(
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kotsadm-s3",
						Namespace: "default",
					},
					Data: map[string][]byte{
						"access-key-id":           []byte("minio"),
						"secret-access-key":       []byte("minio"),
						"endpoint":                []byte("minio"),
						"object-store-cluster-ip": []byte("minio"),
						"untouched":               []byte("untouched"),
					},
				},
			),
			accessKey: "accessKey",
			secretKey: "secretKey",
			hostname:  "hostname",
			endpoint:  "endpoint",
			wantErr:   false,
			validate: func(t *testing.T, client kubernetes.Interface) {
				kotsadms3, err := client.CoreV1().Secrets("default").Get(context.Background(), "kotsadm-s3", metav1.GetOptions{})
				req := require.New(t)
				req.NoError(err)

				req.Equal("accessKey", string(kotsadms3.Data["access-key-id"]))
				req.Equal("secretKey", string(kotsadms3.Data["secret-access-key"]))
				req.Equal("hostname", string(kotsadms3.Data["endpoint"]))
				req.Equal("endpoint", string(kotsadms3.Data["object-store-cluster-ip"]))
				req.Equal("untouched", string(kotsadms3.Data["untouched"]))
				return
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := require.New(t)

			err := updateKotsadmObjectStore(context.TODO(), tt.client, nil, tt.accessKey, tt.secretKey, tt.hostname, tt.endpoint)
			if tt.wantErr {
				req.Error(err)
				return
			} else {
				req.NoError(err)
			}

			// validate cluster state
			tt.validate(t, tt.client)
		})
	}
}

func Test_updateRegistryObjectStore(t *testing.T) {
	tests := []struct {
		name      string
		client    kubernetes.Interface
		accessKey string
		secretKey string
		hostname  string
		endpoint  string
		wantErr   bool
		validate  func(t *testing.T, client kubernetes.Interface)
	}{
		{
			name: "no registry, no update",
			client: fake.NewSimpleClientset(
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "kurl",
					},
				},
			),
			accessKey: "accessKey",
			secretKey: "secretKey",
			hostname:  "hostname",
			endpoint:  "endpoint",
			wantErr:   false,
			validate: func(t *testing.T, client kubernetes.Interface) {
				_, err := client.CoreV1().Secrets("kurl").Get(context.Background(), "registry-s3-secret", metav1.GetOptions{})
				require.Error(t, err)
				if !util.IsNotFoundErr(err) {
					t.Fatalf("expected not found error, got %s", err.Error())
				}

				_, err = client.CoreV1().ConfigMaps("kurl").Get(context.Background(), "registry-config", metav1.GetOptions{})
				require.Error(t, err)
				if !util.IsNotFoundErr(err) {
					t.Fatalf("expected not found error, got %s", err.Error())
				}

				return
			},
		},
		{
			name: "minio to ekco",
			client: fake.NewSimpleClientset(
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "kurl",
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "registry-config",
						Namespace: "kurl",
					},
					Data: map[string]string{
						"config.yml": `health:
  storagedriver:
	enabled: true
	interval: 10s
	threshold: 3
auth:
  htpasswd:
	realm: basic-realm
	path: /auth/htpasswd
http:
  addr: :443
  headers:
	X-Content-Type-Options:
	- nosniff
  tls:
	certificate: /etc/pki/registry.crt
	key: /etc/pki/registry.key
log:
  fields:
	service: registry
  accesslog:
	disabled: true
storage:
  delete:
	enabled: true
  redirect:
	disable: true
  s3:
	region: us-east-1
	regionendpoint: http://10.96.2.101/
	bucket: docker-registry
  cache:
	blobdescriptor: inmemory
  maintenance:
	uploadpurging:
	  enabled: false
version: 0.1`,
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "registry-s3-secret",
						Namespace: "kurl",
					},
					Data: map[string][]byte{
						"access-key-id":           []byte("minio"),
						"secret-access-key":       []byte("minio"),
						"object-store-cluster-ip": []byte("minio"),
						"object-store-hostname":   []byte("minio"),
						"untouched":               []byte("untouched"),
					},
				},
			),
			accessKey: "accessKey",
			secretKey: "secretKey",
			hostname:  "hostname",
			endpoint:  "endpoint",
			wantErr:   false,
			validate: func(t *testing.T, client kubernetes.Interface) {
				req := require.New(t)
				config, err := client.CoreV1().ConfigMaps("kurl").Get(context.Background(), "registry-config", metav1.GetOptions{})
				req.NoError(err)

				req.Contains(config.Data["config.yml"], "regionendpoint: http://endpoint/")

				secret, err := client.CoreV1().Secrets("kurl").Get(context.Background(), "registry-s3-secret", metav1.GetOptions{})
				req.NoError(err)
				req.Equal("accessKey", string(secret.Data["access-key-id"]))
				req.Equal("secretKey", string(secret.Data["secret-access-key"]))
				req.Equal("endpoint", string(secret.Data["object-store-cluster-ip"]))
				req.Equal("hostname", string(secret.Data["object-store-hostname"]))
				req.Equal("untouched", string(secret.Data["untouched"]))
				return
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := require.New(t)

			err := updateRegistryObjectStore(context.TODO(), tt.client, nil, tt.accessKey, tt.secretKey, tt.hostname, tt.endpoint)
			if tt.wantErr {
				req.Error(err)
				return
			} else {
				req.NoError(err)
			}

			// validate cluster state
			tt.validate(t, tt.client)
		})
	}
}
