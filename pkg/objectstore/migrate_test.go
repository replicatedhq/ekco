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
