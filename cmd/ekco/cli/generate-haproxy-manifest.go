package cli

import (
	"github.com/replicatedhq/ekco/pkg/internallb"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func GenerateHAProxyManifestCmd(v *viper.Viper) *cobra.Command {
	primaries := &[]string{}
	var loadBalancerPort int
	var filename string

	cmd := &cobra.Command{
		Use:   "generate-haproxy-manifest",
		Short: "Generate HAProxy manifest file for the internal load balancer",
		Args:  cobra.ExactArgs(0),
		PreRun: func(cmd *cobra.Command, args []string) {
			v.BindPFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, args []string) error {

			err := internallb.GenerateHAProxyManifest(loadBalancerPort, filename, *primaries...)
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&loadBalancerPort, "load-balancer-port", 6444, "Load balancer port")
	cmd.Flags().StringSliceVar(primaries, "primary-host", []string{}, "Kubernetes API server IP or hostname")
	cmd.Flags().StringVar(&filename, "file", "/etc/kubernetes/manifests/haproxy", "Filename for the haproxy static pod manifest")

	return cmd
}
