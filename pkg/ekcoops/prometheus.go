package ekcoops

import (
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
)

type patchUInt32Value struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value uint32 `json:"value"`
}

func (o *Operator) prometheusAutoscaler(currentNodeCount, previousNodeCount int) error {
	if currentNodeCount == previousNodeCount {
		return nil
	}
	prometheus, _ := o.controller.Config.PrometheusV1.Namespace("monitoring").Get("k8s", metav1.GetOptions{})
	alertManager, _ := o.controller.Config.AlertManagerV1.Namespace("monitoring").Get("prometheus-alertmanager", metav1.GetOptions{})
	if prometheus == nil || alertManager == nil {
		return nil
	}

	alertManagersPatch := []patchUInt32Value{{
		Op:    "replace",
		Path:  "/spec/replicas",
		Value: min(3, uint32(currentNodeCount)),
	}}
	alertManagersPayload, err := json.Marshal(alertManagersPatch)
	_, err = o.controller.Config.AlertManagerV1.Namespace("monitoring").Patch("prometheus-alertmanager", types.JSONPatchType, alertManagersPayload, metav1.PatchOptions{})
	if err != nil {
		return errors.Wrap(err, "unable to scale AlertManager in response to node watch event")
	}

	prometheusPatch := []patchUInt32Value{{
		Op:    "replace",
		Path:  "/spec/replicas",
		Value: min(2, uint32(currentNodeCount)),
	}}
	prometheusPayload, err := json.Marshal(prometheusPatch)
	_, err = o.controller.Config.PrometheusV1.Namespace("monitoring").Patch("k8s", types.JSONPatchType, prometheusPayload, metav1.PatchOptions{})
	if err != nil {
		return errors.Wrap(err, "Unable to scale Prometheus in response to watch node event.")
	}
	return nil
}

func min(a, b uint32) uint32 {
	if a <= b {
		return a
	}
	return b
}
