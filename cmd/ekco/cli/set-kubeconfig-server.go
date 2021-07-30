package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/replicatedhq/ekco/pkg/kubeconfig"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func SetKubeconfigServerCmd(v *viper.Viper) *cobra.Command {
	var hostEtcDir string
	var server string
	var admin bool

	cmd := &cobra.Command{
		Use:   "set-kubeconfig-server",
		Short: "Update kubeconfig clients to use a new server",
		Args:  cobra.ExactArgs(0),
		PreRun: func(cmd *cobra.Command, args []string) {
			v.BindPFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()

			kubeletConf := filepath.Join(hostEtcDir, "kubernetes/kubelet.conf")
			if err := kubeconfig.SetServer(kubeletConf, server); err != nil {
				return fmt.Errorf("update kubelet.conf: %v", err)
			}

			if admin {
				adminConf := filepath.Join(hostEtcDir, "kubernetes/admin.conf")
				if err := kubeconfig.SetServer(adminConf, server); err != nil {
					return fmt.Errorf("update admin.conf: %v", err)
				}
			}

			if err := kubeconfig.RestartKubelet(ctx); err != nil {
				return fmt.Errorf("restart kubelet: %v", err)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&server, "server", "", "Address of Kubernetes API server including protocol, e.g. https://localhost:6444")
	cmd.Flags().StringVar(&hostEtcDir, "host-etc-dir", "/etc", "Etc directory where kubeconfigs reside")
	cmd.Flags().BoolVar(&admin, "admin", false, "Update /etc/kubernetes/admin.conf")

	return cmd
}
