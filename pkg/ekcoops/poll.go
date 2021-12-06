package ekcoops

import (
	"context"
	"k8s.io/client-go/dynamic"
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

	prometheusClient := dynamicClient.Resource(prometheusGvr).Namespace("monitoring")
	alertManagerClient := dynamicClient.Resource(alertManagerGvr).Namespace("monitoring")

	lastNodeCount := 0
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

			currentNodeCount := len(nodeList.Items)
			if currentNodeCount != lastNodeCount {
				prometheus, _ := prometheusClient.Get("k8s", metav1.GetOptions{})
				alertManager, _ := alertManagerClient.Get("prometheus-alertmanager", metav1.GetOptions{})
				if prometheus != nil && alertManager != nil {
					err = prometheusAutoscaler(nodeList, prometheusClient, alertManagerClient)
					if err != nil {
						logger.Error("Failure running prometheus autoscaler.", err)
					}
				}
			}

		case <-ctx.Done():
			ticker.Stop()
			return
		}
		i++
	}
}
