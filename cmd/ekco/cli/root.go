package cli

import (
	"fmt"
	"strings"

	"github.com/replicatedhq/ekco/pkg/version"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func RootCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ekco",
		Short: "Embedded Kurl cluster operator (ekco) version",
		Long:  `Print the version of this ekco command`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			v.BindPFlags(cmd.PersistentFlags())
		},
		PreRun: func(cmd *cobra.Command, args []string) {
			v.BindPFlags(cmd.Flags())
		},
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Embedded Kurl cluster operator (ekco) %s\n", version.Version())
		},
	}

	cmd.PersistentFlags().String("log_level", "info", "Log level")

	cobra.OnInitialize(initConfig(v))

	cmd.AddCommand(OperatorCmd(v))

	v.BindPFlags(cmd.Flags())
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	return cmd
}

func InitAndExecute() error {
	return RootCmd(viper.New()).Execute()
}

func initConfig(v *viper.Viper) func() {
	return func() {
		v.AutomaticEnv()
	}
}
