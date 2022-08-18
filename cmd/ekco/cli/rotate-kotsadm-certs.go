package cli

import (
	"time"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/logger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func RotateKotsadmCertsCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rotate-kotsadm-certs",
		Short: "Rotate kotsadm certs",
		Long:  `Manually rotate kotsadm certificates`,
		Args:  cobra.ExactArgs(0),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return v.BindPFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			config, err := initEKCOConfig(v)
			if err != nil {
				return errors.Wrap(err, "failed to initialize config")
			}
			config.RotateCertsTTL = v.GetDuration("ttl")

			log, err := logger.FromViper(v)
			if err != nil {
				return errors.Wrap(err, "failed to initialize logger")
			}

			clusterController, err := initClusterController(config, log)
			if err != nil {
				return errors.Wrap(err, "failed to initialize cluster controller")
			}

			return clusterController.RotateKurlProxyCert()
		},
	}

	cmd.Flags().Duration("ttl", time.Hour*24*180, "Rotate any certificates expiring within this timeframe")

	return cmd
}
