package cli

import (
	"context"
	"time"

	"github.com/replicatedhq/ekco/pkg/cluster"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	"github.com/replicatedhq/ekco/pkg/logger"
	cephv1 "github.com/rook/rook/pkg/client/clientset/versioned/typed/ceph.rook.io/v1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
)

func OperatorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "operator",
		Short: "Replicated Embedded Kubernetes operator",
		Long:  `Manage nodes and storage of a Replicated Embbedded Kubernetes cluster`,
		PreRun: func(cmd *cobra.Command, args []string) {
			viper.BindPFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			config := &ekcoops.Config{}
			if err := viper.Unmarshal(config); err != nil {
				return err
			}

			log := logger.NewLogger()

			clientConfig, err := restclient.InClusterConfig()
			if err != nil {
				return err
			}

			client, err := kubernetes.NewForConfig(clientConfig)
			if err != nil {
				return err
			}

			rookcephclient, err := cephv1.NewForConfig(clientConfig)
			if err != nil {
				return err
			}

			clusterController := cluster.NewController(cluster.ControllerConfig{
				Client:          client,
				ClientConfig:    clientConfig,
				CephV1:          rookcephclient,
				CertificatesDir: config.CertificatesDir,
			}, log)

			operator := ekcoops.New(*config, client, clusterController, log)

			operator.Poll(context.Background(), config.ReconcileInterval)

			return nil
		},
	}

	cmd.Flags().Duration("node_unreachable_toleration", time.Hour, "Minimum node unavailable time until considered dead")
	cmd.Flags().Bool("purge_dead_nodes", false, "Automatically purge lost nodes after unavailable_toleration")
	cmd.Flags().Int("min_ready_master_nodes", 2, "Minimum number of ready master nodes required for auto-purge")
	cmd.Flags().Int("min_ready_worker_nodes", 0, "Minimum number of ready worker nodes required for auto-purge")
	cmd.Flags().Bool("maintain_rook_storage_nodes", false, "Add and remove nodes to the ceph cluster and scale replication of pools")
	cmd.Flags().String("ceph_block_pool", "replicapool", "Name of CephBlockPool to manage if maintain_rook_storage_nodes is enabled")
	cmd.Flags().String("ceph_filesystem", "rook-shared-fs", "Name of CephFilesystem to manage if maintain_rook_storage_nodes is enabled")
	cmd.Flags().String("ceph_object_store", "replicated", "Name of CephObjectStore to manage if maintain_rook_storage_nodes is enabled")
	cmd.Flags().Int("min_ceph_pool_replication", 1, "Minimum replication factor of ceph_block_pool and ceph_filesystem pools")
	cmd.Flags().Int("max_ceph_pool_replication", 3, "Maximum replication factor of ceph_block_pool and ceph_filesystem pools")
	cmd.Flags().String("certificates_dir", "/etc/kubernetes/pki", "Kubernetes certificates directory")
	cmd.Flags().Duration("reconcile_interval", time.Minute, "Frequency to run the operator's control loop")
	cmd.Flags().String("log_level", "info", "Log level")

	return cmd
}
