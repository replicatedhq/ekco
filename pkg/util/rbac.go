package util

import (
	"context"

	"github.com/pkg/errors"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// KubeadmClusterAdminsGroup is the group name used by kubeadm for certificate rotation
	KubeadmClusterAdminsGroup = "kubeadm:cluster-admins"
	// KubeadmClusterAdminsBinding is the name of the ClusterRoleBinding
	KubeadmClusterAdminsBinding = "kubeadm:cluster-admins"
)

// ClusterRoleBindingExists checks if a ClusterRoleBinding exists
func ClusterRoleBindingExists(ctx context.Context, client kubernetes.Interface, name string) (bool, error) {
	_, err := client.RbacV1().ClusterRoleBindings().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if kerrors.IsNotFound(err) {
			return false, nil
		}
		return false, errors.Wrapf(err, "get clusterrolebinding %s", name)
	}
	return true, nil
}

// EnsureKubeadmClusterAdminsBinding validates that the kubeadm:cluster-admins ClusterRoleBinding exists
// This binding should be created during EKCO deployment, not at runtime
func EnsureKubeadmClusterAdminsBinding(ctx context.Context, client kubernetes.Interface) error {
	exists, err := ClusterRoleBindingExists(ctx, client, KubeadmClusterAdminsBinding)
	if err != nil {
		return errors.Wrap(err, "check if kubeadm:cluster-admins binding exists")
	}
	if !exists {
		return errors.New("kubeadm:cluster-admins ClusterRoleBinding not found - this should be created during EKCO deployment")
	}
	return nil
}

// ValidateKubeadmClusterAdminsBinding validates that the kubeadm:cluster-admins binding
// exists and is properly configured
func ValidateKubeadmClusterAdminsBinding(ctx context.Context, client kubernetes.Interface) error {
	binding, err := client.RbacV1().ClusterRoleBindings().Get(ctx, KubeadmClusterAdminsBinding, metav1.GetOptions{})
	if err != nil {
		return errors.Wrapf(err, "get clusterrolebinding %s", KubeadmClusterAdminsBinding)
	}

	// Check if it references cluster-admin role
	if binding.RoleRef.Name != "cluster-admin" {
		return errors.Errorf("kubeadm:cluster-admins binding references role %s, expected cluster-admin", binding.RoleRef.Name)
	}

	// Check if it has the correct group
	found := false
	for _, subject := range binding.Subjects {
		if subject.Kind == rbacv1.GroupKind && subject.Name == KubeadmClusterAdminsGroup {
			found = true
			break
		}
	}
	if !found {
		return errors.Errorf("kubeadm:cluster-admins binding does not contain group %s", KubeadmClusterAdminsGroup)
	}

	return nil
}

 