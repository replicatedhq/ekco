package cli

import (
	"fmt"

	"github.com/replicatedhq/ekco/pkg/internallb"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func GenerateHAProxyConfigCmd(v *viper.Viper) *cobra.Command {
	primaries := &[]string{}

	cmd := &cobra.Command{
		Use:   "generate-haproxy-config",
		Short: "Generate HAProxy config file for the internal load balancer",
		Args:  cobra.ExactArgs(0),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return v.BindPFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, args []string) error {

			out, err := internallb.GenerateHAProxyConfig(*primaries...)
			if err != nil {
				return err
			}

			fmt.Print(string(out))

			return nil
		},
	}

	cmd.Flags().StringSliceVar(primaries, "primary-host", []string{}, "Kubernetes API server IP or hostname")

	return cmd
}
