package util

import (
	"context"
	"fmt"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ScaleDownPrometheus scales down the prometheus-operator deployment to 0 replicas
// we scale down the operator instead of patching the custom resource because this way we don't have to import the prometheus-operator CRD
// the prometheus StatefulSet will be scaled down by pvmigrate (and then restored)
func ScaleDownPrometheus(ctx context.Context, client kubernetes.Interface) error {
	return scalePrometheusOperator(ctx, client, 0)
}

// ScaleUpPrometheus scales up the prometheus-operator deployment to 1 replica
func ScaleUpPrometheus(ctx context.Context, client kubernetes.Interface) error {
	return scalePrometheusOperator(ctx, client, 1)
}

func scalePrometheusOperator(ctx context.Context, client kubernetes.Interface, replicas int32) error {
	operator, err := client.AppsV1().Deployments("monitoring").Get(ctx, "prometheus-operator", metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		} else {
			return fmt.Errorf("failed to get prometheus-operator deployment: %v", err)
		}
	}
	operator.Spec.Replicas = &replicas
	_, err = client.AppsV1().Deployments("monitoring").Update(ctx, operator, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to scale prometheus-operator deployment to %d replicas: %v", replicas, err)
	}

	return nil
}
