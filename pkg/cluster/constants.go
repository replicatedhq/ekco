package cluster

const (
	RookCephNS                   = "rook-ceph"
	CephClusterName              = "rook-ceph"
	RookCephSharedFSMetadataPool = "rook-shared-fs-metadata"
	RookCephSharedFSDataPool     = "rook-shared-fs-data0"
	CephDeviceHealthMetricsPool  = "device_health_metrics"

	RookCephObjectStoreRootPool = ".rgw.root"
)

var (
	RookCephObjectStoreMetadataPools = []string{
		// .rgw.root (rootPool) is appended to this slice where needed
		"rgw.control",
		"rgw.meta",
		"rgw.log",
		"rgw.buckets.index",
		"rgw.buckets.non-ec",
	}
	RookCephObjectStoreDataPools = []string{
		"rgw.buckets.data",
	}
)
