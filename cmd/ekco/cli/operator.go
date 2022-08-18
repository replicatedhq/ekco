package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	"github.com/replicatedhq/ekco/pkg/internallb"
	"github.com/replicatedhq/ekco/pkg/logger"
	"github.com/replicatedhq/ekco/pkg/version"
	"github.com/replicatedhq/ekco/pkg/webhook"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func OperatorCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "operator",
		Short: "Embedded kURL cluster operator",
		Long:  `Manage nodes and storage of an embedded kURL cluster`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return v.BindPFlags(cmd.Flags())
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

			log.Infof("Embedded kURL cluster operator (EKCO) %s", version.Version())

			clusterController, err := initClusterController(config, log)
			if err != nil {
				return errors.Wrap(err, "failed to initialize cluster controller")
			}

			if config.RookPriorityClass != "" || len(config.PodImageOverrides) > 0 {
				podImageOverrides := map[string]string{}
				for _, override := range config.PodImageOverrides {
					parts := strings.Split(override, "=")
					if len(parts) != 2 {
						return fmt.Errorf("Cannot parse pod image override %q", override)
					}
					podImageOverrides[parts[0]] = parts[1]
				}
				webhookServer, err := webhook.NewServer(clusterController.Config.Client, "kurl", config.RookPriorityClass, podImageOverrides, log)
				if err != nil {
					return errors.Wrap(err, "initialize webhook server")
				}
				go webhookServer.Run()
			}
			if config.RookPriorityClass == "" {
				if err := webhook.RemoveRookPriority(clusterController.Config.Client); err != nil {
					return errors.Wrap(err, "delete webhook config")
				}
			}
			if len(config.PodImageOverrides) == 0 {
				if err := webhook.RemovePodImageOverrides(clusterController.Config.Client); err != nil {
					return errors.Wrap(err, "delete webhook config")
				}
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
	cmd.Flags().Bool("reconcile_rook_mds_placement", true, "Reconcile CephFilesystem MDS placement when the cluster is scaled beyond one node")
	cmd.Flags().String("rook_version", "1.4.3", "Version of Rook to manage")
	cmd.Flags().String("rook_priority_class", "node-critical", "Priority class to add to Rook 1.0 Deployments and DaemonSets. Will be created if not found")
	cmd.Flags().Int("min_ceph_pool_replication", 1, "Minimum replication factor of ceph_block_pool and ceph_filesystem pools")
	cmd.Flags().Int("max_ceph_pool_replication", 3, "Maximum replication factor of ceph_block_pool and ceph_filesystem pools")
	cmd.Flags().String("certificates_dir", "/etc/kubernetes/pki", "Kubernetes certificates directory")
	cmd.Flags().Duration("reconcile_interval", time.Minute, "Frequency to run the operator's control loop")
	cmd.Flags().String("rotate_certs_namespace", "kurl", "Namespace where certificate rotation pods will run")
	cmd.Flags().String("rotate_certs_image", "replicated/ekco:latest", "Image to use in certificate rotation pods")
	cmd.Flags().Duration("rotate_certs_check_interval", time.Hour*24, "How often to check for certs that need to be rotated")
	cmd.Flags().Duration("rotate_certs_ttl", time.Hour*24*30, "Rotate any certificates expiring within this timeframe")
	cmd.Flags().Bool("rotate_certs", true, "Enable automatic certificate rotation")
	cmd.Flags().String("registry_cert_namespace", "kurl", "Namespace where the registry is running")
	cmd.Flags().String("registry_cert_secret", "registry-pki", "Name of the secret that holds the registry certificate key pair")
	cmd.Flags().String("kurl_proxy_cert_namespace", "default", "Namespace where kurl proxy is running")
	cmd.Flags().String("kurl_proxy_cert_secret", "kotsadm-tls", "Name of the secret that holds the kurl proxy key pair")
	cmd.Flags().String("kotsadm_kubelet_cert_namespace", "default", "Namespace where kotsadm is running")
	cmd.Flags().String("kotsadm_kubelet_cert_secret", "default", "Name of the secret that holds the kubelet client certificate used by kotsadm")
	cmd.Flags().String("contour_cert_namespace", "projectcontour", "Namespace where contour is running")
	cmd.Flags().String("contour_cert_secret", "contourcert", "Name of the secret that holds the contour certificate")
	cmd.Flags().String("envoy_cert_secret", "envoycert", "Name of the secret that holds the envoy certificate")
	cmd.Flags().String("host_task_namespace", "kurl", "Namespace where pods performing host tasks will run")
	cmd.Flags().String("host_task_image", "replicated/ekco:latest", "Image to use in host task pods")
	cmd.Flags().Bool("enable_internal_load_balancer", false, "Run haproxy on localhost forwarding to all in-cluster Kubernetes API servers")
	cmd.Flags().String("internal_load_balancer_haproxy_image", internallb.HAProxyImage, "HAProxy container image to use for internal load balancer")
	cmd.Flags().StringSlice("pod_image_overrides", nil, "Image to override in pods")
	cmd.Flags().Bool("auto_approve_kubelet_csrs", false, "Enable auto approval of kubelet Certificate Signing Requests")

	return cmd
}
