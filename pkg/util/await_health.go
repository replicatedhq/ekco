package util

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// AwaitDeploymentReady waits for a deployment to have all replicas ready and available
func AwaitDeploymentReady(ctx context.Context, client kubernetes.Interface, namespace string, name string) error {
	for {
		dep, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get deployment %s in %s: %v", name, namespace, err)
		}
		if dep.Status.ReadyReplicas == dep.Status.Replicas &&
			dep.Status.AvailableReplicas == dep.Status.Replicas &&
			dep.Status.UnavailableReplicas == 0 &&
			dep.Status.UpdatedReplicas == dep.Status.Replicas {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// AwaitStatefulsetReady waits for a statefulset to have all replicas ready and available
func AwaitStatefulsetReady(ctx context.Context, client kubernetes.Interface, namespace string, name string) error {
	for {
		ss, err := client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get StatefulSet %s in %s: %v", name, namespace, err)
		}
		if ss.Status.ReadyReplicas == ss.Status.Replicas &&
			ss.Status.AvailableReplicas == ss.Status.Replicas &&
			ss.Status.UpdatedReplicas == ss.Status.Replicas {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
