package cli

import (
	"context"
	"fmt"
	"log"

	"github.com/replicatedhq/ekco/pkg/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ChangeLoadBalancerCmd(v *viper.Viper) *cobra.Command {
	var internal bool
	var exclude string
	var server string

	cmd := &cobra.Command{
		Use:   "change-load-balancer",
		Short: "Handle Kubernetes load balancer address change on all nodes",
		Args:  cobra.ExactArgs(0),
		PreRun: func(cmd *cobra.Command, args []string) {
			v.BindPFlags(cmd.Flags())
		},
		// Output must end with "Result:" for parsing in scripts
		Run: func(cmd *cobra.Command, args []string) {
			ctx := context.Background()

			config, err := initEKCOConfig(v)
			if err != nil {
				log.Fatalf("Result: Failed to initialize config: %v", err)
			}

			log, err := logger.FromViper(v)
			if err != nil {
				log.Fatalf("Result: Failed to initialize logger: %v", err)
			}

			clusterController, err := initClusterController(config, log)
			if err != nil {
				log.Fatalf("Result: Failed to initialize cluster controller: %v", err)
			}

			nodeList, err := clusterController.Config.Client.CoreV1().Nodes().List(metav1.ListOptions{})
			if err != nil {
				log.Fatalf("Result: Failed to initialize cluster controller: %v", err)
			}

			if internal {
				err := clusterController.UpdateInternalLB(ctx, nodeList.Items)
				if err != nil {
					log.Fatalf("Result: Failed to start internal load balancer: %v", err)
				}
			}

			for _, node := range nodeList.Items {
				if node.Name == exclude {
					continue
				}
				err := clusterController.SetKubeconfigServer(ctx, node, server)
				if err != nil {
					log.Fatalf("Result: Failed to update kubeconfigs: %v", err)
				}
			}

			fmt.Println("Result: success") // Scripts rely on this exact string to indicate success
		},
	}

	cmd.Flags().StringVar(&server, "server", "", "New load balancer address, including protocol")
	cmd.Flags().BoolVar(&internal, "internal", false, "Enable the internal load balancer")
	cmd.Flags().StringVar(&exclude, "exclude", "", "Node to exclude")

	cmd.MarkFlagRequired("server")

	return cmd
}
