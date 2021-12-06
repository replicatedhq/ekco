package ekcoops

import (
	"github.com/pkg/errors"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/client-go/dynamic"
)

var (
	alertManagerGvr = schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "alertmanagers",
	}
	prometheusGvr = schema.GroupVersionResource{
		Group:    "monitoring.coreos.com",
		Version:  "v1",
		Resource: "prometheuses",
	}
)

type patchUInt32Value struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value uint32 `json:"value"`
}

func prometheusAutoscaler(nodeList *v1.NodeList, prometheusClient, alertManagerClient dynamic.ResourceInterface) error {
	nodeCount := uint32(len(nodeList.Items))

	alertManagersPatch := []patchUInt32Value{{
		Op:    "replace",
		Path:  "/spec/replicas",
		Value: min(3, nodeCount),
	}}
	alertManagersPayload, err := json.Marshal(alertManagersPatch)
	_, err = alertManagerClient.Patch("prometheus-alertmanager", types.JSONPatchType, alertManagersPayload, metav1.PatchOptions{})
	if err != nil {
		return errors.Wrap(err, "unable to scale AlertManager in response to node watch event")
	}

	prometheusPatch := []patchUInt32Value{{
		Op:    "replace",
		Path:  "/spec/replicas",
		Value: min(2, nodeCount),
	}}
	prometheusPayload, err := json.Marshal(prometheusPatch)
	_, err = prometheusClient.Patch("k8s", types.JSONPatchType, prometheusPayload, metav1.PatchOptions{})
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
