package cli

import (
	"fmt"
	"strings"

	"github.com/replicatedhq/ekco/pkg/version"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func RootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ekco",
		Short: "Embedded Kurl cluster operator (ekco) version",
		Long:  `Print the version of this ekco command`,
		PreRun: func(cmd *cobra.Command, args []string) {
			viper.BindPFlags(cmd.Flags())
		},
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Embedded Kurl cluster operator (ekco) %s\n", version.Version())
		},
	}

	cobra.OnInitialize(initConfig)

	cmd.AddCommand(OperatorCmd())

	viper.BindPFlags(cmd.Flags())
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))

	return cmd
}

func InitAndExecute() error {
	return RootCmd().Execute()
}

func initConfig() {
	viper.AutomaticEnv()
}
