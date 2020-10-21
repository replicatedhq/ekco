package cli

import (
	"log"

	"github.com/replicatedhq/ekco/pkg/version"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func RootCmd(v *viper.Viper) *cobra.Command {
	var cfgFile string

	cmd := &cobra.Command{
		Use:   "ekco",
		Short: "Embedded kURL cluster operator (EKCO) version",
		Long:  `Print the version of this ECKO command`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			v.BindPFlags(cmd.PersistentFlags())
		},
		PreRun: func(cmd *cobra.Command, args []string) {
			v.BindPFlags(cmd.Flags())
		},
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Printf("Embedded kURL cluster operator (EKCO) %s\n", version.Version())
		},
	}

	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Config file (default is /etc/ekco/config.yaml)")
	cmd.PersistentFlags().String("log_level", "info", "Log level")

	cmd.AddCommand(OperatorCmd(v))
	cmd.AddCommand(PurgeNodeCmd(v))
	cmd.AddCommand(RotateCertsCmd(v))
	cmd.AddCommand(RegenCertCmd(v))

	cobra.OnInitialize(initConfig(v, cfgFile))
	v.AutomaticEnv()

	return cmd
}

func InitAndExecute() error {
	return RootCmd(viper.New()).Execute()
}

func initConfig(v *viper.Viper, cfgFile string) func() {
	return func() {
		if cfgFile != "" {
			v.SetConfigFile(cfgFile)
		} else {
			v.AddConfigPath("/etc/ekco")
			v.AddConfigPath("$HOME")
			v.AddConfigPath(".")
			v.SetConfigName("config")
		}

		if err := v.ReadInConfig(); err != nil {
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				log.Panicf("Failed to read config: %v", err)
			}
		}
	}
}
