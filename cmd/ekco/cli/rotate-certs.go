package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/replicatedhq/ekco/pkg/rotate"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func RotateCertsCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate-certs",
		Short: "Rotate certs ",
		Long:  `Rotate certs in a kURL cluster`,
		Args:  cobra.ExactArgs(0),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return v.BindPFlags(cmd.Flags())
		},
		Run: func(cmd *cobra.Command, args []string) {
			ttl := v.GetDuration("ttl")
			hostname := v.GetString("hostname")

			if err := rotate.RotateCerts(ttl, hostname); err != nil {
				// warning - this output is parsed by the ekco operator
				fmt.Printf("Error: %v\n", err)
				os.Exit(1)
			}
		},
	}

	cmd.Flags().Duration("ttl", time.Hour*24*180, "Rotate any certificates expiring within this timeframe")
	cmd.Flags().String("hostname", "", "Hostname where this pod is running")

	return cmd
}
