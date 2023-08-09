package rook

import (
	"strings"

	"github.com/blang/semver"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
)

// GetCephVersion returns the semver.Version of Ceph as reported by the cephv1.CephCluster
// resource.
func GetCephVersion(cluster cephv1.CephCluster) (semver.Version, error) {
	if cluster.Status.CephVersion == nil {
		// status.cephVersion is nil for Rook 1.0.4
		parts := strings.Split(cluster.Spec.CephVersion.Image, ":")
		imageTag := parts[len(parts)-1]
		ver, err := semver.Parse(strings.TrimPrefix(imageTag, "v"))
		if err != nil {
			return semver.Version{}, err
		}
		ver.Pre = []semver.PRVersion{{VersionNum: 0, IsNum: true}}
		return ver, nil
	}

	return semver.Parse(cluster.Status.CephVersion.Version)
}

// CephClusterManagedByHelm returns true if the CephCluster spec was applied by Helm
func CephClusterManagedByHelm(cluster cephv1.CephCluster) bool {
	labels := cluster.Labels

	managedBy, ok := labels["app.kubernetes.io/managed-by"]
	if ok {
		if strings.ToLower(managedBy) == "helm" {
			return true
		}
	}
	return false
}
