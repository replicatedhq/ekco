package cli

import (
	"fmt"

	"github.com/replicatedhq/ekco/pkg/internallb"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func GenerateHAProxyConfigCmd(v *viper.Viper) *cobra.Command {
	primaries := &[]string{}
	var loadBalancerPort int

	cmd := &cobra.Command{
		Use:   "generate-haproxy-config",
		Short: "Generate HAProxy config file for the internal load balancer",
		Args:  cobra.ExactArgs(0),
		PreRun: func(cmd *cobra.Command, args []string) {
			v.BindPFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, args []string) error {

			out, err := internallb.GenerateHAProxyConfig(loadBalancerPort, *primaries...)
			if err != nil {
				return err
			}

			fmt.Print(string(out))

			return nil
		},
	}

	cmd.Flags().IntVar(&loadBalancerPort, "load-balancer-port", 6444, "Load balancer port")
	cmd.Flags().StringSliceVar(primaries, "primary-host", []string{}, "Kubernetes API server IP or hostname")

	return cmd
}
