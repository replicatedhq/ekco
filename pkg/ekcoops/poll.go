package ekcoops

import (
	"context"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Poll will run the reconcile function on an interval
func (o *Operator) Poll(ctx context.Context, interval time.Duration) {
	logger := o.controller.Log
	ticker := time.NewTicker(interval)

	dynamicClient, err := dynamic.NewForConfig(o.controller.Config.ClientConfig)
	if err != nil {
		logger.Error("Failed creating Kubernetes client.", err)
	}

	installerList, err := dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "cluster.kurl.sh",
		Version:  "v1beta1",
		Resource: "installers",
	}).
		List(metav1.ListOptions{})
	if err != nil {
		logger.Error("Failed to fetch.", err)
	}

	var (
		nodeWatcher        watch.Interface
		prometheusClient   dynamic.ResourceInterface
		alertManagerClient dynamic.ResourceInterface
	)

	if len(installerList.Items) != 0 {
		var currentInstaller unstructured.Unstructured
		for _, installer := range installerList.Items {
			selectedInstallerTimestamp := currentInstaller.GetCreationTimestamp()
			nextInstallerTimestamp := installer.GetCreationTimestamp()
			if (&selectedInstallerTimestamp).Before(&nextInstallerTimestamp) {
				currentInstaller = installer
			}
		}

		prometheusClient = dynamicClient.Resource(prometheusGvr).Namespace("monitoring")
		alertManagerClient = dynamicClient.Resource(alertManagerGvr).Namespace("monitoring")

		prometheus, _ := prometheusClient.Get("k8s", metav1.GetOptions{})
		alertManager, _ := alertManagerClient.Get("prometheus-alertmanager", metav1.GetOptions{})

		if prometheus != nil && alertManager != nil {
			nodeWatcher, err = o.controller.Config.Client.CoreV1().Nodes().Watch(metav1.ListOptions{})
			if err != nil {
				logger.Error("Failed to start watcher on nodes.", err)
			}
		}
	}

	i := 0
	for {
		select {
		case <-ticker.C:
			nodeList, err := o.client.CoreV1().Nodes().List(metav1.ListOptions{})
			if err != nil {
				logger.Infof("Skipping reconcile: failed to list nodes: %v", err)
				continue
			}
			doFullReconcile := i%60 == 0
			err = o.Reconcile(nodeList.Items, doFullReconcile)
			if err != nil {
				logger.Infof("Reconcile failed: %v", err)
				continue
			}
		case event := <-nodeWatcher.ResultChan():
			if event.Type != watch.Added && event.Type != watch.Deleted {
				continue
			}
			err := startPrometheusAutoscaler(o.controller.Config.Client, prometheusClient, alertManagerClient)
			if err != nil {
				logger.Error("Failure running prometheus autoscaler.", err)
			}
		case <-ctx.Done():
			ticker.Stop()
			return
		}
		i++
	}
}

type patchUInt32Value struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value uint32 `json:"value"`
}

func startPrometheusAutoscaler(kubeClient kubernetes.Interface, prometheusClient, alertManagerClient dynamic.ResourceInterface) error {
	nodeList, err := kubeClient.CoreV1().Nodes().List(metav1.ListOptions{})
	if err != nil {
		return err
	}
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
