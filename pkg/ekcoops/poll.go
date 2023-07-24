package ekcoops

import (
	"context"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Poll will run the reconcile function on an interval
func (o *Operator) Poll(ctx context.Context, interval time.Duration) {
	logger := o.controller.Log
	ticker := time.NewTicker(interval)

	if err := o.onLaunch(ctx); err != nil {
		logger.Infof("on launch failed: %v", err)
	}

	i := 0
	for {
		select {
		case <-ticker.C:
			nodeList, err := o.client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
			if err != nil {
				logger.Infof("Skipping reconcile: failed to list nodes: %v", err)
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
			doFullReconcile := i%60 == 0
			err = o.Reconcile(ctx, nodeList.Items, doFullReconcile)
			cancel()
			if err != nil {
				logger.Infof("Reconcile failed: %v", err)
				continue
			}
		case <-ctx.Done():
			ticker.Stop()
			return
		}
		i++
	}
}

func (o *Operator) onLaunch(ctx context.Context) error {
	if o.config.RotateCerts {
		if err := o.RotateCerts(ctx, true); err != nil {
			return errors.Wrap(err, "rotate certs")
		}
	}
	return nil
}
