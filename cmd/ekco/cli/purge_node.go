package cli

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/cluster"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	"github.com/replicatedhq/ekco/pkg/logger"
	"github.com/replicatedhq/ekco/pkg/util"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func PurgeNodeCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "purge-node [name]",
		Short: "Purge node ",
		Long:  `Manually purge a Kurl cluster node`,
		Args:  cobra.ExactArgs(1),
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

			return purgeNode(args[0], config, clusterController)
		},
	}

	cmd.Flags().Int("min_ready_master_nodes", 2, "Minimum number of ready master nodes required for auto-purge")
	cmd.Flags().Int("min_ready_worker_nodes", 0, "Minimum number of ready worker nodes required for auto-purge")
	cmd.Flags().Bool("maintain_rook_storage_nodes", false, "Add and remove nodes to the ceph cluster and scale replication of pools")
	cmd.Flags().String("rook_version", "1.4.3", "Version of Rook to manage")
	cmd.Flags().String("certificates_dir", "/etc/kubernetes/pki", "Kubernetes certificates directory")

	return cmd
}

func purgeNode(nodeName string, config *ekcoops.Config, clusterController *cluster.Controller) error {
	nodeList, err := clusterController.Config.Client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to list nodes")
	}

	readyMasters, readyWorkers := util.NodeReadyCounts(nodeList.Items)
	if readyMasters <= config.MinReadyMasterNodes {
		for _, node := range nodeList.Items {
			if node.Name == nodeName && util.NodeIsMaster(node) && util.NodeIsReady(node) {
				return fmt.Errorf("cannot purge master: %d ready masters", readyMasters)
			}
		}
	}
	if readyWorkers <= config.MinReadyWorkerNodes {
		for _, node := range nodeList.Items {
			if node.Name == nodeName && !util.NodeIsMaster(node) && util.NodeIsReady(node) {
				return fmt.Errorf("cannot purge worker: %d ready workers", readyWorkers)
			}
		}
	}

	err = clusterController.PurgeNode(context.Background(), nodeName, config.MaintainRookStorageNodes)
	return errors.Wrap(err, "failed to purge node")
}
