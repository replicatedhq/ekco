package ekcoops

import "k8s.io/apimachinery/pkg/runtime/schema"

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
