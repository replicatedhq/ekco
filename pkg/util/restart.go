package util

import (
	"context"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// RestartDeployment restarts a deployment by deleting all of its pods, one at a time
func RestartDeployment(ctx context.Context, client kubernetes.Interface, namespace string, name string) error {
	dep, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get deployment %s in %s: %v", name, namespace, err)
	}

	// get the selector for the deployment
	selector, err := metav1.LabelSelectorAsSelector(dep.Spec.Selector)
	if err != nil {
		return fmt.Errorf("failed to get selector for deployment %s in %s: %v", name, namespace, err)
	}

	// get the pods for the deployment
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return fmt.Errorf("failed to get pods for deployment %s in %s: %v", name, namespace, err)
	}

	for _, pod := range pods.Items {
		// delete the pod
		err := client.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("failed to delete pod %s in %s: %v", pod.Name, namespace, err)
		}

		// wait for the deployment to be ready again
		err = AwaitDeploymentReady(ctx, client, namespace, name)
		if err != nil {
			return fmt.Errorf("failed to wait for deployment %s in %s to be ready: %v", name, namespace, err)
		}
	}

	return nil
}

// RestartStatefulset restarts a statefulset by deleting all of its pods, one at a time
func RestartStatefulset(ctx context.Context, client kubernetes.Interface, namespace string, name string) error {
	return nil
}
