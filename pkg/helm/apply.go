package helm

import (
	"context"
	"fmt"
	"io"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
)

func ApplyChart(chartBytes io.Reader, values, namespace string) error {
	chart, err := loader.LoadArchive(chartBytes)
	if err != nil {
		return err
	}

	env := cli.New()
	env.SetNamespace(namespace)

	actConfig := action.Configuration{}
	actConfig.Init(env.RESTClientGetter(), namespace, "", nil)

	act := action.NewInstall(&actConfig)
	act.Namespace = namespace
	act.ReleaseName = "rook-ceph"
	if _, err := act.RunWithContext(context.TODO(), chart, nil); err != nil {
		return fmt.Errorf("unable to install chart: %w", err)
	}

	return nil
}
