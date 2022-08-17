package ekcoops

import (
	"time"
)

type Config struct {
	// how long a Node must be unreachable before considered dead
	NodeUnreachableToleration time.Duration `mapstructure:"node_unreachable_toleration"`
	// don't purge if it will result in less than this many ready masters
	MinReadyMasterNodes int `mapstructure:"min_ready_master_nodes"`
	// don't purge if it will result in less than this many ready workers
	MinReadyWorkerNodes int `mapstructure:"min_ready_worker_nodes"`

	// whether to purge dead nodes from the cluster automatically
	PurgeDeadNodes bool `mapstructure:"purge_dead_nodes"`
	// whether to force delete terminating pods on dead nodes automatically
	ClearDeadNodes bool `mapstructure:"clear_dead_nodes"`

	// whether to maintain the list of nodes to use in the CephCluster config. With Rook 1.0
	// Replicated maintains the list to obviate the need to restart the operator, but with
	// earlier versions of Rook we just set `useAllNodes` to true. This also enables control of
	// the replication factor of ceph pools, scaling up and down with the number of nodes in the
	// cluster.
	MaintainRookStorageNodes bool `mapstructure:"maintain_rook_storage_nodes"`
	// when set, only nodes with this label will be added to the ceph cluster
	RookStorageNodesLabel string `mapstructure:"rook_storage_nodes_label"`
	// names and levels of ceph pools to maintain if MaintainRookStorageNodes is enabled
	CephBlockPool          string `mapstructure:"ceph_block_pool"`
	CephFilesystem         string `mapstructure:"ceph_filesystem"`
	CephObjectStore        string `mapstructure:"ceph_object_store"`
	MinCephPoolReplication int    `mapstructure:"min_ceph_pool_replication"`
	MaxCephPoolReplication int    `mapstructure:"max_ceph_pool_replication"`
	RookVersion            string `mapstructure:"rook_version"`
	// when set, priority class to be applied to rook deployments and daemonsets
	RookPriorityClass string `mapstructure:"rook_priority_class"`
	// Whether to reconcile CephFilesystem MDS placement when the cluster is scaled beyond one
	// node. MDS pod anti-affinity is made more lenient at install time by kURL to allow for single
	// node Rook upgrades. This setting will revert that change upon the addition of a second node.
	ReconcileRookMDSPlacement bool `mapstructure:"reconcile_rook_mds_placement"`

	// kubernetes certificates directory
	CertificatesDir string `mapstructure:"certificates_dir"`

	ReconcileInterval time.Duration `mapstructure:"reconcile_interval"`

	RotateCerts                           bool          `mapstructure:"rotate_certs"`
	RotateCertsImage                      string        `mapstructure:"rotate_certs_image"`
	RotateCertsNamespace                  string        `mapstructure:"rotate_certs_namespace"`
	RotateCertsCheckInterval              time.Duration `mapstructure:"rotate_certs_check_interval"`
	RotateCertsTTL                        time.Duration `mapstructure:"rotate_certs_ttl"`
	RegistryCertNamespace                 string        `mapstructure:"registry_cert_namespace"`
	RegistryCertSecret                    string        `mapstructure:"registry_cert_secret"`
	KurlProxyCertNamespace                string        `mapstructure:"kurl_proxy_cert_namespace"`
	KurlProxyCertSecret                   string        `mapstructure:"kurl_proxy_cert_secret"`
	KotsadmKubeletCertNamespace           string        `mapstructure:"kotsadm_kubelet_cert_namespace"`
	KotsadmKubeletCertSecret              string        `mapstructure:"kotsadm_kubelet_cert_secret"`
	ContourCertNamespace                  string        `mapstructure:"contour_cert_namespace"`
	ContourCertSecret                     string        `mapstructure:"contour_cert_secret"`
	EnvoyCertSecret                       string        `mapstructure:"envoy_cert_secret"`
	EnableInternalLoadBalancer            bool          `mapstructure:"enable_internal_load_balancer"`
	InternalLoadBalancerHAProxyImage      string        `mapstructure:"internal_load_balancer_haproxy_image"`
	InternalLoadBalancerPort              int           `mapstructure:"internal_load_balancer_port"`
	HostTaskImage                         string        `mapstructure:"host_task_image"`
	HostTaskNamespace                     string        `mapstructure:"host_task_namespace"`
	PodImageOverrides                     []string      `mapstructure:"pod_image_overrides"`
	AutoApproveKubeletCertSigningRequests bool          `mapstructure:"auto_approve_kubelet_csrs"`
}
