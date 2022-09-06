package cluster

import (
	"context"
	"time"

	multierror "github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	DefaultContourNamespace          = "projectcontour"
	DefaultEnvoyPodsNotReadyDuration = 5 * time.Minute
)

// RestartFailedEnvoyPods will forcefully delete envoy pods that have fallen into an unrecoverable
// state for at least EnvoyPodsNotReadyDuration.
func (c *Controller) RestartFailedEnvoyPods(ctx context.Context) error {
	logger := c.Log.With("phase", "RestartFailedEnvoyPods")

	if !c.Config.RestartFailedEnvoyPods {
		logger.Debugf("disabled, skipping")
		return nil
	}
	if c.Config.ContourNamespace == "" {
		logger.Debug("contour namespace not set, skipping")
		return nil
	}

	logger.Debug("reconciling failed envoy pods")

	selector := labels.SelectorFromSet(map[string]string{"app": "envoy"})
	pods, err := c.Config.Client.CoreV1().Pods(c.Config.ContourNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector.String(),
	})
	if err != nil {
		return errors.Wrap(err, "list envoy pods")
	}

	var multiErr error

	for _, pod := range pods.Items {
		logger = logger.With("pod", pod.Name)
		if shouldRestartEnvoyPod(pod.Status, c.Config.EnvoyPodsNotReadyDuration) {
			logger.Debug("forcefully deleting failed envoy pod")
			err := c.Config.Client.CoreV1().Pods(c.Config.ContourNamespace).Delete(ctx, pod.Name, *metav1.NewDeleteOptions(0))
			if err != nil {
				multiErr = multierror.Append(multiErr, errors.Wrapf(err, "forcefully deleting pod %s", pod.Name))
			} else {
				logger.Info("forcefully deleted failed envoy pod")
			}
		}
	}

	return multiErr
}

// shouldRestartEnvoyPod will return true should the following conditions be satisfied:
// - the pod is running
// - shutdown manager is ready and envoy is not ready (due to failing readiness checks)
// - envoy has been notready for at least envoy_pods_not_ready_duration (default 5 minutes)
func shouldRestartEnvoyPod(podStatus corev1.PodStatus, notReadyDuration time.Duration) bool {
	if podStatus.Phase != corev1.PodRunning {
		return false
	}

	before := time.Now().Add(-notReadyDuration)

	shutdownManagerReady, envoyNotReady := false, false
	for _, status := range podStatus.ContainerStatuses {
		switch status.Name {
		case "shutdown-manager":
			if status.Ready &&
				status.State.Running != nil &&
				status.State.Running.StartedAt.Time.Before(before) {
				shutdownManagerReady = true
			}
		case "envoy":
			if !status.Ready &&
				status.State.Running != nil &&
				status.State.Running.StartedAt.Time.Before(before) {
				envoyNotReady = true
			}
		}
	}

	if !(shutdownManagerReady && envoyNotReady) {
		return false
	}

	for _, condition := range podStatus.Conditions {
		if condition.Type == corev1.ContainersReady &&
			condition.Reason == "ContainersNotReady" &&
			condition.Status == corev1.ConditionFalse {
			return condition.LastTransitionTime.Time.Before(before)
		}
	}

	return false
}
