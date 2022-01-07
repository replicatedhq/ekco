package ekcoops

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Poll will run the reconcile function on an interval
func (o *Operator) Poll(ctx context.Context, interval time.Duration) {
	logger := o.controller.Log
	ticker := time.NewTicker(interval)

	previousNodeCount := 0
	i := 0
	for {
		select {
		case <-ticker.C:
			nodeList, err := o.client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
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
			err = o.prometheusAutoscaler(currentNodeCount, previousNodeCount)
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
