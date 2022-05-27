package util

import (
	v1 "k8s.io/api/core/v1"
)

const MasterRoleLabel = "node-role.kubernetes.io/master"
const ControlPlaneRoleLabel = "node-role.kubernetes.io/control-plane"
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
	ok := false

	if _, ok = node.ObjectMeta.Labels[MasterRoleLabel]; ok {
		return ok
	}
	if _, ok = node.ObjectMeta.Labels[ControlPlaneRoleLabel]; ok {
		return ok
	}
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

func NodeInternalIP(node v1.Node) string {
	for _, addr := range node.Status.Addresses {
		if addr.Type == v1.NodeInternalIP {
			return addr.Address
		}
	}
	return ""
}
