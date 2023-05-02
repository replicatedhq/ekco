package util

import (
	"context"
	"fmt"
	"time"

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

	// set a label on the Deployment to trigger a rolling restart
	if dep.Spec.Template.ObjectMeta.Labels == nil {
		dep.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}
	dep.Spec.Template.ObjectMeta.Labels["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
	_, err = client.AppsV1().Deployments(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to set restart label on Deployment %s in %s: %v", name, namespace, err)
	}

	// wait for the Deployment to be ready again
	err = AwaitDeploymentReady(ctx, client, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to wait for Deployment %s in %s to be ready: %v", name, namespace, err)
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

	if sts.Spec.Template.ObjectMeta.Labels == nil {
		sts.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}
	sts.Spec.Template.ObjectMeta.Labels["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
	_, err = client.AppsV1().StatefulSets(namespace).Update(ctx, sts, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to set restart label on StatefulSet %s in %s: %v", name, namespace, err)
	}

	// wait for the StatefulSet to be ready again
	err = AwaitStatefulSetReady(ctx, client, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to wait for StatefulSet %s in %s to be ready: %v", name, namespace, err)
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

	if dep.Spec.Template.ObjectMeta.Labels == nil {
		dep.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}
	dep.Spec.Template.ObjectMeta.Labels["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)
	_, err = client.AppsV1().DaemonSets(namespace).Update(ctx, dep, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to set restart label on DaemonSet %s in %s: %v", name, namespace, err)
	}

	// wait for the DaemonSet to be ready again
	err = AwaitDaemonSetReady(ctx, client, namespace, name)
	if err != nil {
		return fmt.Errorf("failed to wait for DaemonSet %s in %s to be ready: %v", name, namespace, err)
	}

	return nil
}
