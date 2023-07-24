package util

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/replicatedhq/ekco/pkg/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// ScalePrometheus scales the prometheus operator to the given number of replicas
func ScalePrometheus(ctx context.Context, promClient dynamic.NamespaceableResourceInterface, replicas int64) error {
	prometheus, _ := promClient.Namespace("monitoring").Get(ctx, "k8s", metav1.GetOptions{})
	if prometheus == nil {
		return nil
	}

	currentPrometheusReplicas, ok := prometheus.Object["spec"].(map[string]interface{})["replicas"].(int64)
	if !ok {
		return fmt.Errorf("failed to parse prometheus replicas")
	}

	if currentPrometheusReplicas != replicas {
		prometheusPatch := []k8s.JSONPatchOperation{{
			Op:    k8s.JSONPatchOpReplace,
			Path:  "/spec/replicas",
			Value: replicas,
		}}
		prometheusPayload, err := json.Marshal(prometheusPatch)
		if err != nil {
			return fmt.Errorf("failed to marshal json: %w", err)
		}
		_, err = promClient.Namespace("monitoring").Patch(ctx, "k8s", types.JSONPatchType, prometheusPayload, metav1.PatchOptions{})
		if err != nil {
			return fmt.Errorf("failed to patch prometheus to %d replicas: %w", replicas, err)
		}
	}

	return nil
}

// ScaleAlertManager scales the prometheus operator to the given number of replicas
func ScaleAlertManager(ctx context.Context, alertClient dynamic.NamespaceableResourceInterface, replicas int64) error {
	alertManager, _ := alertClient.Namespace("monitoring").Get(ctx, "prometheus-alertmanager", metav1.GetOptions{})
	if alertManager == nil {
		return nil
	}

	currentAlertManagerReplicas, ok := alertManager.Object["spec"].(map[string]interface{})["replicas"].(int64)
	if !ok {
		return fmt.Errorf("failed to parse alert manager replicas")
	}

	if currentAlertManagerReplicas != replicas {
		alertManagersPatch := []k8s.JSONPatchOperation{{
			Op:    k8s.JSONPatchOpReplace,
			Path:  "/spec/replicas",
			Value: replicas,
		}}
		alertManagersPayload, err := json.Marshal(alertManagersPatch)
		if err != nil {
			return fmt.Errorf("failed to marshal json: %w", err)
		}
		_, err = alertClient.Namespace("monitoring").Patch(ctx, "prometheus-alertmanager", types.JSONPatchType, alertManagersPayload, metav1.PatchOptions{})
		if err != nil {
			return fmt.Errorf("failed to patch alertmanager: %w", err)
		}
	}

	return nil
}
