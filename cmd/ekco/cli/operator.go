package cli

import (
	"context"
	"fmt"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	"github.com/replicatedhq/ekco/pkg/logger"
	"github.com/replicatedhq/ekco/pkg/webhook"
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

			err = startPrometheusAutoscaler(clusterController.Config.Client, clusterController.Config.ClientConfig, log)
			if err != nil {
				log.Error("Unable to start prometheus autoscaler.", err)
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
	cmd.Flags().String("host_task_namespace", "kurl", "Namespace where pods performing host tasks will run")
	cmd.Flags().String("host_task_image", "replicated/ekco:latest", "Image to use in host task pods")
	cmd.Flags().Bool("enable_internal_load_balancer", false, "Run haproxy on localhost forwarding to all in-cluster Kubernetes API servers")
	cmd.Flags().StringSlice("pod_image_overrides", nil, "Image to override in pods")

	return cmd
}

type patchUInt32Value struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value uint32 `json:"value"`
}

func startPrometheusAutoscaler(kubeClient kubernetes.Interface, restClientConfig *restclient.Config, logger *zap.SugaredLogger) error {
	dynamicClient, err := dynamic.NewForConfig(restClientConfig)
	if err != nil {
		return err
	}

	installerList, err := dynamicClient.Resource(schema.GroupVersionResource{
		Group:    "cluster.kurl.sh",
		Version:  "v1beta1",
		Resource: "installers",
	}).
		List(metav1.ListOptions{})
	if err != nil {
		return err
	}

	if len(installerList.Items) == 0 {
		return nil
	}

	var currentInstaller unstructured.Unstructured
	for _, installer := range installerList.Items {
		selectedInstallerTimestamp := currentInstaller.GetCreationTimestamp()
		nextInstallerTimestamp := installer.GetCreationTimestamp()
		if (&selectedInstallerTimestamp).Before(&nextInstallerTimestamp) {
			currentInstaller = installer
		}
	}

	spec := currentInstaller.Object["spec"].(map[string]interface{})
	if _, ok := spec["prometheus"]; !ok {
		logger.Info("Prometheus autoscaler disabled: Prometheus not detected in kurl install.")
		return nil
	} else {
		logger.Info("Prometheus autoscaler enabled: Prometheus was detected in kurl install.")
	}

	nodeWatcher, err := kubeClient.CoreV1().Nodes().Watch(metav1.ListOptions{})
	if err != nil {
		return err
	}

	go func() {
		for {
			event := <-nodeWatcher.ResultChan()
			if event.Type != watch.Added && event.Type != watch.Deleted {
				continue
			}
			nodeList, err := kubeClient.CoreV1().Nodes().List(metav1.ListOptions{})
			if err != nil {
				logger.Error("Unable to list nodes.", err)
				return
			}
			nodeCount := uint32(len(nodeList.Items))

			alertManagersPatch := []patchUInt32Value{{
				Op:    "replace",
				Path:  "/spec/replicas",
				Value: min(3, nodeCount),
			}}
			alertManagersPayload, err := json.Marshal(alertManagersPatch)
			_, err = dynamicClient.Resource(schema.GroupVersionResource{
				Group:    "monitoring.coreos.com",
				Version:  "v1",
				Resource: "alertmanagers",
			}).
				Namespace("monitoring").
				Patch("prometheus-alertmanager", types.JSONPatchType, alertManagersPayload, metav1.PatchOptions{})
			if err != nil {
				logger.Error(err, "Unable to scale AlertManager in response to node watch event.", err)
				return
			}

			prometheusPatch := []patchUInt32Value{{
				Op:    "replace",
				Path:  "/spec/replicas",
				Value: min(2, nodeCount),
			}}
			prometheusPayload, err := json.Marshal(prometheusPatch)
			_, err = dynamicClient.Resource(schema.GroupVersionResource{
				Group:    "monitoring.coreos.com",
				Version:  "v1",
				Resource: "prometheuses",
			}).
				Namespace("monitoring").
				Patch("k8s", types.JSONPatchType, prometheusPayload, metav1.PatchOptions{})
			if err != nil {
				logger.Error("Unable to scale Prometheus in response to watch node event.", err)
				return
			}
		}
	}()

	return nil
}

func min(a, b uint32) uint32 {
	if a <= b {
		return a
	}
	return b
}
