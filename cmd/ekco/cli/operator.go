package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	"github.com/replicatedhq/ekco/pkg/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func OperatorCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "operator",
		Short: "Embedded kURL cluster operator",
		Long:  `Manage nodes and storage of an embedded kURL cluster`,
		PreRun: func(cmd *cobra.Command, args []string) {
			v.BindPFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := initEKCOConfig(v)
			if err != nil {
				return errors.Wrap(err, "failed to initialize config")
			}

			log, err := logger.FromViper(v)
			if err != nil {
				return errors.Wrap(err, "failed to initialize logger")
			}

			clusterController, err := initClusterController(config, log)
			if err != nil {
				return errors.Wrap(err, "failed to initialize cluster controller")
			}

			operator := ekcoops.New(*config, clusterController.Config.Client, clusterController, log)

			sigs := make(chan os.Signal, 1)
			signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				sig := <-sigs
				log.Infof("Operator got signal %s, exiting on next poll", sig)
				cancel()
			}()

			operator.Poll(ctx, config.ReconcileInterval)

			return nil
		},
	}

	cmd.Flags().Duration("node_unreachable_toleration", time.Hour, "Minimum node unavailable time until considered dead")
	cmd.Flags().Bool("purge_dead_nodes", false, "Automatically purge lost nodes after unavailable_toleration")
	cmd.Flags().Int("min_ready_master_nodes", 2, "Minimum number of ready master nodes required for auto-purge")
	cmd.Flags().Int("min_ready_worker_nodes", 0, "Minimum number of ready worker nodes required for auto-purge")
	cmd.Flags().Bool("maintain_rook_storage_nodes", false, "Add and remove nodes to the ceph cluster and scale replication of pools")
	cmd.Flags().String("rook_storage_nodes_label", "", "When set, only nodes with this label will be added to the ceph cluster")
	cmd.Flags().String("ceph_block_pool", "replicapool", "Name of CephBlockPool to manage if maintain_rook_storage_nodes is enabled")
	cmd.Flags().String("ceph_filesystem", "rook-shared-fs", "Name of CephFilesystem to manage if maintain_rook_storage_nodes is enabled")
	cmd.Flags().String("ceph_object_store", "replicated", "Name of CephObjectStore to manage if maintain_rook_storage_nodes is enabled")
	cmd.Flags().Int("min_ceph_pool_replication", 1, "Minimum replication factor of ceph_block_pool and ceph_filesystem pools")
	cmd.Flags().Int("max_ceph_pool_replication", 3, "Maximum replication factor of ceph_block_pool and ceph_filesystem pools")
	cmd.Flags().String("certificates_dir", "/etc/kubernetes/pki", "Kubernetes certificates directory")
	cmd.Flags().Duration("reconcile_interval", time.Minute, "Frequency to run the operator's control loop")

	return cmd
}
