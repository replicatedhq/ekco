package ekcoops

import (
	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/util"
)

func (o *Operator) ReconcilePrometheus(nodeCount int) error {
	desiredPrometheusReplicas := min(2, int64(nodeCount))
	o.log.Debugf("Ensuring k8s prometheus replicas are set to %d", desiredPrometheusReplicas)
	err := util.ScalePrometheus(o.controller.Config.PrometheusV1, desiredPrometheusReplicas)
	if err != nil {
		return errors.Wrap(err, "failed to scale prometheus operator")
	}

	desiredAlertManagerReplicas := min(3, int64(nodeCount))
	o.log.Debugf("Ensuring prometheus alert manager replicas are set to %d", desiredAlertManagerReplicas)
	err = util.ScaleAlertManager(o.controller.Config.AlertManagerV1, desiredAlertManagerReplicas)
	if err != nil {
		return errors.Wrap(err, "failed to scale alert manager operator")
	}

	return nil
}

func min(a, b int64) int64 {
	if a <= b {
		return a
	}
	return b
}
