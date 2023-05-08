package ekcoops

import (
	"testing"
	"time"

	"github.com/replicatedhq/ekco/pkg/util"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestOperatorIsDead(t *testing.T) {
	tests := []struct {
		name     string
		operator *Operator
		answer   bool
		node     corev1.Node
	}{
		{
			name:     "Dead",
			operator: &Operator{config: Config{NodeUnreachableToleration: time.Minute}},
			answer:   true,
			node: corev1.Node{
				Spec: corev1.NodeSpec{
					Taints: []corev1.Taint{
						{
							Key:       util.UnreachableTaint,
							TimeAdded: &metav1.Time{Time: time.Now().Add(-time.Hour)},
						},
					},
				},
			},
		},
		{
			name:     "Unreachable, not dead yet",
			operator: &Operator{config: Config{NodeUnreachableToleration: time.Hour}},
			answer:   false,
			node: corev1.Node{
				Spec: corev1.NodeSpec{
					Taints: []corev1.Taint{
						{
							Key:       util.UnreachableTaint,
							TimeAdded: &metav1.Time{Time: time.Now().Add(-time.Minute)},
						},
					},
				},
			},
		},
		{
			name:     "Untainted",
			operator: &Operator{config: Config{NodeUnreachableToleration: time.Minute}},
			answer:   false,
			node:     corev1.Node{},
		},
		{
			name:     "Different taint",
			operator: &Operator{config: Config{NodeUnreachableToleration: time.Minute}},
			answer:   false,
			node: corev1.Node{
				Spec: corev1.NodeSpec{
					Taints: []corev1.Taint{
						{
							Key:       "kubernetes.io/hostname",
							TimeAdded: &metav1.Time{Time: time.Now().Add(-time.Hour)},
						},
					},
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := test.operator.isDead(test.node)
			if output != test.answer {
				t.Errorf("got %t, want %t", output, test.answer)
			}
		})
	}
}

func Test_shouldUseNodeForStorage(t *testing.T) {
	type args struct {
		node                  corev1.Node
		cephCluster           *cephv1.CephCluster
		rookStorageNodesLabel string
		manageNodes           bool
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "ready",
			args: args{
				node:                  corev1.Node{},
				rookStorageNodesLabel: "",
			},
			want: true,
		},
		{
			name: "not ready",
			args: args{
				node: corev1.Node{
					Spec: corev1.NodeSpec{
						Taints: []corev1.Taint{
							{Key: util.NotReadyTaint},
						},
					},
				},
				rookStorageNodesLabel: "",
			},
			want: false,
		},
		{
			name: "ready and label",
			args: args{
				node: corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"node-role.kubernetes.io/rook": "true"},
					},
				},
				rookStorageNodesLabel: "node-role.kubernetes.io/rook=true",
			},
			want: true,
		},
		{
			name: "not ready and label",
			args: args{
				node: corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{"node-role.kubernetes.io/rook": "true"},
					},
					Spec: corev1.NodeSpec{
						Taints: []corev1.Taint{
							{Key: util.NotReadyTaint},
						},
					},
				},
				rookStorageNodesLabel: "node-role.kubernetes.io/rook=true",
			},
			want: false,
		},
		{
			name: "ready and no label",
			args: args{
				node:                  corev1.Node{},
				rookStorageNodesLabel: "node-role.kubernetes.io/rook=true",
			},
			want: false,
		},
		{
			name: "ready and rook nodes array and in cephcluster",
			args: args{
				node:                  corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node1"}},
				cephCluster:           &cephv1.CephCluster{Spec: cephv1.ClusterSpec{Storage: cephv1.StorageScopeSpec{Nodes: []cephv1.Node{{Name: "node1"}}}}},
				rookStorageNodesLabel: "",
				manageNodes:           true,
			},
			want: true,
		},
		{
			name: "ready and rook nodes array and not in cephcluster",
			args: args{
				node:                  corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node2"}},
				cephCluster:           &cephv1.CephCluster{Spec: cephv1.ClusterSpec{Storage: cephv1.StorageScopeSpec{Nodes: []cephv1.Node{{Name: "node1"}}}}},
				rookStorageNodesLabel: "",
				manageNodes:           true,
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldUseNodeForStorage(tt.args.node, tt.args.cephCluster, tt.args.rookStorageNodesLabel, tt.args.manageNodes); got != tt.want {
				t.Errorf("shouldUseNodeForStorage() = %v, want %v", got, tt.want)
			}
		})
	}
}
