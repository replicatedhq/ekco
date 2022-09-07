package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClearNode force deletes pods stuck in Terminating state on a single node.
func (c *Controller) ClearNode(ctx context.Context, nodeName string) error {
	c.Log.Debugf("Deleting terminating pods on node %s", nodeName)

	opts := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
	}
	pods, err := c.Config.Client.CoreV1().Pods("").List(ctx, opts)
	if err != nil {
		return errors.Wrapf(err, "list all pods on node %s", nodeName)
	}

	for _, pod := range pods.Items {
		if pod.DeletionTimestamp.IsZero() {
			// pod not deleted
			continue
		}
		if pod.DeletionTimestamp.Add(time.Second * 30).After(time.Now()) {
			// pod may still gracefully terminate
			continue
		}
		c.Log.Infof("Force deleting pod %s/%s on node %s", pod.Namespace, pod.Name, nodeName)
		err := c.Config.Client.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, *metav1.NewDeleteOptions(0))
		if err != nil {
			return errors.Wrapf(err, "delete pod %s/%s", pod.Namespace, pod.Name)
		}
	}

	return nil
}
