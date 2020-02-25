package ekcoops

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Poll will run the reconcile function on an interval
func (o *Operator) Poll(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)

	for {
		select {
		case <-ticker.C:
			o.log.Debugf("Poll now")

			nodeList, err := o.client.CoreV1().Nodes().List(metav1.ListOptions{})
			if err != nil {
				o.log.Infof("Skipping reconcile: failed to list nodes: %v", err)
				continue
			}
			err = o.Reconcile(nodeList.Items)
			if err != nil {
				o.log.Infof("Reconcile failed: %v", err)
				continue
			}
		case <-ctx.Done():
			ticker.Stop()
			return
		}
	}
}
