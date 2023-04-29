package cluster

import (
	"context"
	"testing"

	"github.com/replicatedhq/ekco/pkg/cluster/types"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientsetfake "k8s.io/client-go/kubernetes/fake"
	kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
)

func Test_removeKubeadmEndpoint(t *testing.T) {
	tests := []struct {
		name                 string
		endpoint             string
		clusterResources     []runtime.Object
		expectedRemovedIP    string
		expectedRemainingIPs []string
		expectedConfigMap    corev1.ConfigMap
		wantErr              bool
	}{
		{
			name:     "when endpoint is removed expect kubeadm configmap to be mutated",
			endpoint: "master-test-2", // removing master-test-2
			clusterResources: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      kubeadmconstants.KubeadmConfigConfigMap,
						Namespace: metav1.NamespaceSystem,
					},
					TypeMeta: metav1.TypeMeta{},
					Data: map[string]string{
						clusterStatusConfigMapKey: `kind: ClusterStatus
apiVersion: kubeadm.k8s.io/v1beta2
apiEndpoints:
  master-test-1:
    advertiseAddress: 10.128.0.126
    bindPort: 6443
  master-test-2:
    advertiseAddress: 10.128.0.63
    bindPort: 6443
`,
					},
				},
			},
			expectedRemovedIP:    "10.128.0.63",
			expectedRemainingIPs: []string{"10.128.0.126"},
			expectedConfigMap: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kubeadmconstants.KubeadmConfigConfigMap,
					Namespace: metav1.NamespaceSystem,
				},
				TypeMeta: metav1.TypeMeta{},
				Data: map[string]string{
					clusterStatusConfigMapKey: `kind: ClusterStatus
apiVersion: kubeadm.k8s.io/v1beta2
apiEndpoints:
  master-test-1:
    advertiseAddress: 10.128.0.126
    bindPort: 6443
`,
				},
			},
		},
		{
			name:     "when ClusterStatus field is incompatible then correct it on next update",
			endpoint: "master-test-2", // removing master-test-2
			clusterResources: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      kubeadmconstants.KubeadmConfigConfigMap,
						Namespace: metav1.NamespaceSystem,
					},
					TypeMeta: metav1.TypeMeta{},
					Data: map[string]string{
						clusterStatusConfigMapKey: `
apiendpoints:
  master-test-1:
    advertiseAddress: 10.128.0.126
    bindport: 6443
  master-test-2:
    advertiseaddress: 10.128.0.63
    bindport: 6443
typemeta:
  kind: ClusterStatus
  apiversion: kubeadm.k8s.io/v1beta2
`,
					},
				},
			},
			expectedRemovedIP:    "10.128.0.63",
			expectedRemainingIPs: []string{"10.128.0.126"},
			expectedConfigMap: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kubeadmconstants.KubeadmConfigConfigMap,
					Namespace: metav1.NamespaceSystem,
				},
				TypeMeta: metav1.TypeMeta{},
				Data: map[string]string{
					clusterStatusConfigMapKey: `kind: ClusterStatus
apiVersion: kubeadm.k8s.io/v1beta2
apiEndpoints:
  master-test-1:
    advertiseAddress: 10.128.0.126
    bindPort: 6443
`,
				},
			},
		},
		{
			name:     "when clusterStatus field does not exist then no updates to kubeadm configmap",
			endpoint: "master-test-2", // removing master-test-2
			clusterResources: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      kubeadmconstants.KubeadmConfigConfigMap,
						Namespace: metav1.NamespaceSystem,
					},
					TypeMeta: metav1.TypeMeta{},
					Data:     map[string]string{},
				},
			},
			expectedConfigMap: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kubeadmconstants.KubeadmConfigConfigMap,
					Namespace: metav1.NamespaceSystem,
				},
				TypeMeta: metav1.TypeMeta{},
				Data:     map[string]string{},
			},
		},
		{
			name: "when ClusterStatus is malformed then correct it as a no-op update (side effect)",
			clusterResources: []runtime.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      kubeadmconstants.KubeadmConfigConfigMap,
						Namespace: metav1.NamespaceSystem,
					},
					TypeMeta: metav1.TypeMeta{},
					Data: map[string]string{
						clusterStatusConfigMapKey: `
apiendpoints:
  master-test-1:
    advertiseaddress: 10.128.0.126
    bindport: 6443
  master-test-2:
    advertiseaddress: 10.128.0.63
    bindport: 6443
typemeta:
  kind: ClusterStatus
  apiversion: kubeadm.k8s.io/v1beta2
`,
					},
				},
			},
			expectedRemainingIPs: []string{"10.128.0.126", "10.128.0.63"},
			expectedConfigMap: corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      kubeadmconstants.KubeadmConfigConfigMap,
					Namespace: metav1.NamespaceSystem,
				},
				TypeMeta: metav1.TypeMeta{},
				Data: map[string]string{
					clusterStatusConfigMapKey: `kind: ClusterStatus
apiVersion: kubeadm.k8s.io/v1beta2
apiEndpoints:
  master-test-1:
    advertiseAddress: 10.128.0.126
    bindPort: 6443
  master-test-2:
    advertiseAddress: 10.128.0.63
    bindPort: 6443
`,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := require.New(t)
			logger, _ := zap.NewProduction()
			client := clientsetfake.NewSimpleClientset(tt.clusterResources...)
			c := NewController(types.ControllerConfig{
				Client: client,
			}, logger.Sugar())

			ip, remainingIPS, err := c.removeKubeadmEndpoint(context.Background(), tt.endpoint)
			if err != nil {
				if tt.wantErr {
					req.Error(err)
				} else {
					req.NoError(err)
				}
			}

			// kubeadm-config configmap gets updated when removing endpoints for k8s < 1.22
			actualKubeadmConfigMap, _ := client.CoreV1().ConfigMaps(metav1.NamespaceSystem).
				Get(context.Background(), kubeadmconstants.KubeadmConfigConfigMap, metav1.GetOptions{})
			req.Equal(tt.expectedRemovedIP, ip)
			req.Equal(tt.expectedRemainingIPs, remainingIPS)
			req.EqualValues(tt.expectedConfigMap, *actualKubeadmConfigMap)
		})
	}
}
