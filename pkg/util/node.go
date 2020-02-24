package util

import (
	v1 "k8s.io/api/core/v1"
)

const MasterRoleLabel = "node-role.kubernetes.io/master"
const UnreachableTaint = "node.kubernetes.io/unreachable"
const NotReadyTaint = "node.kubernetes.io/not-ready"
const NetworkUnavailableTaint = "node.kubernetes.io/network-unavailable"
const UnschedulableTaint = "node.kubernetes.io/unschedulable"

func NodeIsReady(node v1.Node) bool {
	for _, taint := range node.Spec.Taints {
		switch taint.Key {
		case NotReadyTaint:
			return false
		case UnreachableTaint:
			return false
		case NetworkUnavailableTaint:
			return false
		case UnschedulableTaint:
			return false
		}
	}
	return true
}

func NodeIsMaster(node v1.Node) bool {
	_, ok := node.ObjectMeta.Labels[MasterRoleLabel]
	return ok
}

func NodeReadyCounts(nodes []v1.Node) (int, int) {
	masters := 0
	workers := 0

	for _, node := range nodes {
		if !NodeIsReady(node) {
			continue
		}
		if NodeIsMaster(node) {
			masters++
		} else {
			workers++
		}
	}

	return masters, workers
}
