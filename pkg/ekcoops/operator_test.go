package ekcoops

import (
	"testing"
	"time"

	"github.com/replicatedhq/ekco/pkg/util"
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
