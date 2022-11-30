package rook

import (
	"errors"

	"github.com/blang/semver"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
)

// GetCephVersion returns the semver.Version of Ceph as reported by the cephv1.CephCluster
// resource.
func GetCephVersion(cluster cephv1.CephCluster) (semver.Version, error) {
	if cluster.Status.CephVersion == nil {
		return semver.Version{}, errors.New("status.CephVersion is nil")
	}

	return semver.Parse(cluster.Status.CephVersion.Version)
}
