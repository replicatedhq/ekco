package ekcoops

import (
	"context"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
)

func (o *Operator) ReconcilePrometheus(nodeCount int) error {
	prometheus, _ := o.controller.Config.PrometheusV1.Namespace("monitoring").Get(context.TODO(), "k8s", metav1.GetOptions{})
	alertManager, _ := o.controller.Config.AlertManagerV1.Namespace("monitoring").Get(context.TODO(), "prometheus-alertmanager", metav1.GetOptions{})
	if prometheus == nil || alertManager == nil {
		return nil
	}

	desiredPrometheusReplicas := min(2, int64(nodeCount))
	currentPrometheusReplicas, ok := prometheus.Object["spec"].(map[string]interface{})["replicas"].(int64)
	if !ok {
		return errors.New("failed to parse prometheus replicas")
	}

	o.log.Debugf("Ensuring k8s prometheus replicas are set to %d", desiredPrometheusReplicas)

	if currentPrometheusReplicas != desiredPrometheusReplicas {
		prometheusPatch := []k8s.JSONPatchOperation{{
			Op:    k8s.JSONPatchOpReplace,
			Path:  "/spec/replicas",
			Value: desiredPrometheusReplicas,
		}}
		prometheusPayload, err := json.Marshal(prometheusPatch)
		if err != nil {
			return errors.Wrap(err, "failed to marshal json")
		}
		_, err = o.controller.Config.PrometheusV1.Namespace("monitoring").Patch(context.TODO(), "k8s", types.JSONPatchType, prometheusPayload, metav1.PatchOptions{})
		if err != nil {
			return errors.Wrap(err, "failed to patch prometheus")
		}
	}

	desiredAlertManagerReplicas := min(3, int64(nodeCount))
	currentAlertManagerReplicas, ok := alertManager.Object["spec"].(map[string]interface{})["replicas"].(int64)
	if !ok {
		return errors.New("failed to parse alert manager replicas")
	}

	o.log.Debugf("Ensuring prometheus alert manager replicas are set to %d", desiredPrometheusReplicas)

	if currentAlertManagerReplicas != desiredAlertManagerReplicas {
		alertManagersPatch := []k8s.JSONPatchOperation{{
			Op:    k8s.JSONPatchOpReplace,
			Path:  "/spec/replicas",
			Value: desiredAlertManagerReplicas,
		}}
		alertManagersPayload, err := json.Marshal(alertManagersPatch)
		if err != nil {
			return errors.Wrap(err, "failed to marshal json")
		}
		_, err = o.controller.Config.AlertManagerV1.Namespace("monitoring").Patch(context.TODO(), "prometheus-alertmanager", types.JSONPatchType, alertManagersPayload, metav1.PatchOptions{})
		if err != nil {
			return errors.Wrap(err, "failed to patch alertmanager")
		}
	}

	return nil
}

func min(a, b int64) int64 {
	if a <= b {
		return a
	}
	return b
}
