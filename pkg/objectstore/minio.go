package objectstore

import (
	"context"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func IsMinioInUse(ctx context.Context, client kubernetes.Interface, namespace string) (bool, error) {
	if namespace == "" {
		return false, nil
	}
	// if the minio NS does not exist, it is not in use
	_, err := client.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		} else {
			return false, fmt.Errorf("get minio namespace %s: %v", namespace, err)
		}
	}

	// if the minio deployment and statefulset do not exist, there is nothing to migrate
	_, err = client.AppsV1().Deployments(namespace).Get(ctx, "minio", metav1.GetOptions{})
	if err == nil {
		return true, nil
	} else {
		if !k8serrors.IsNotFound(err) {
			return false, fmt.Errorf("get minio deployment in %s: %v", namespace, err)
		}
	}

	_, err = client.AppsV1().StatefulSets(namespace).Get(ctx, "ha-minio", metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		} else {
			return false, fmt.Errorf("get minio statefulset in %s: %v", namespace, err)
		}
	}

	return true, nil
}
