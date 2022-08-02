package cli

import (
	"github.com/replicatedhq/ekco/pkg/internallb"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func GenerateHAProxyManifestCmd(v *viper.Viper) *cobra.Command {
	primaries := &[]string{}
	var filename string
	var image string

	cmd := &cobra.Command{
		Use:   "generate-haproxy-manifest",
		Short: "Generate HAProxy manifest file for the internal load balancer",
		Args:  cobra.ExactArgs(0),
		PreRun: func(cmd *cobra.Command, args []string) {
			v.BindPFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := internallb.GenerateHAProxyManifest(filename, image, internallb.DefaultFileversion)
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&filename, "file", "/etc/kubernetes/manifests/haproxy", "Filename for the haproxy static pod manifest")
	cmd.Flags().StringVar(&image, "image", internallb.HAProxyImage, "Container image for the haproxy static pod manifest")

	cmd.Flags().StringSliceVar(primaries, "primary-host", []string{}, "Kubernetes API server IP or hostname")
	cmd.Flags().MarkDeprecated("primary-host", "this flag is no longer used")
	cmd.Flags().MarkHidden("primary-host")

	return cmd
}
