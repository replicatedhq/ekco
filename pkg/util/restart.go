package util

import (
	"context"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// RestartDeployment restarts a Deployment by deleting all of its pods, one at a time
func RestartDeployment(ctx context.Context, client kubernetes.Interface, namespace string, name string) error {
	dep, err := client.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get Deployment %s in %s: %v", name, namespace, err)
	}

	// get the selector for the Deployment
	selector, err := metav1.LabelSelectorAsSelector(dep.Spec.Selector)
	if err != nil {
		return fmt.Errorf("failed to get selector for Deployment %s in %s: %v", name, namespace, err)
	}

	// get the pods for the Deployment
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return fmt.Errorf("failed to get pods for Deployment %s in %s: %v", name, namespace, err)
	}

	for _, pod := range pods.Items {
		// delete the pod
		err := client.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("failed to delete pod %s in %s: %v", pod.Name, namespace, err)
		}

		// wait for the Deployment to be ready again
		err = AwaitDeploymentReady(ctx, client, namespace, name)
		if err != nil {
			return fmt.Errorf("failed to wait for Deployment %s in %s to be ready: %v", name, namespace, err)
		}
	}

	return nil
}

// RestartStatefulSet restarts a StatefulSet by deleting all of its pods, one at a time
func RestartStatefulSet(ctx context.Context, client kubernetes.Interface, namespace string, name string) error {
	sts, err := client.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get StatefulSet %s in %s: %v", name, namespace, err)
	}

	// get the selector for the StatefulSet
	selector, err := metav1.LabelSelectorAsSelector(sts.Spec.Selector)
	if err != nil {
		return fmt.Errorf("failed to get selector for StatefulSet %s in %s: %v", name, namespace, err)
	}

	// get the pods for the StatefulSet
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return fmt.Errorf("failed to get pods for StatefulSet %s in %s: %v", name, namespace, err)
	}

	for _, pod := range pods.Items {
		// delete the pod
		err := client.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("failed to delete pod %s in %s: %v", pod.Name, namespace, err)
		}

		// wait for the StatefulSet to be ready again
		err = AwaitStatefulSetReady(ctx, client, namespace, name)
		if err != nil {
			return fmt.Errorf("failed to wait for StatefulSet %s in %s to be ready: %v", name, namespace, err)
		}
	}

	return nil
}

// RestartDaemoSet restarts a daemonset by deleting all of its pods, one at a time
func RestartDaemonSet(ctx context.Context, client kubernetes.Interface, namespace string, name string) error {
	dep, err := client.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get DaemonSet %s in %s: %v", name, namespace, err)
	}

	// get the selector for the DaemonSet
	selector, err := metav1.LabelSelectorAsSelector(dep.Spec.Selector)
	if err != nil {
		return fmt.Errorf("failed to get selector for DaemonSet %s in %s: %v", name, namespace, err)
	}

	// get the pods for the DaemonSet
	pods, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return fmt.Errorf("failed to get pods for DaemonSet %s in %s: %v", name, namespace, err)
	}

	for _, pod := range pods.Items {
		// delete the pod
		err := client.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("failed to delete pod %s in %s: %v", pod.Name, namespace, err)
		}

		// wait for the DaemonSet to be ready again
		err = AwaitDaemonSetReady(ctx, client, namespace, name)
		if err != nil {
			return fmt.Errorf("failed to wait for DaemonSet %s in %s to be ready: %v", name, namespace, err)
		}
	}

	return nil
}
