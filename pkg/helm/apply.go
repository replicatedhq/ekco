package helm

import (
	"context"
	"fmt"
	"io"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
)

func ApplyChart(ctx context.Context, chartBytes io.Reader, values map[string]interface{}, namespace, releaseName string) error {
	chart, err := loader.LoadArchive(chartBytes)
	if err != nil {
		return err
	}

	env := cli.New()
	env.SetNamespace(namespace)

	actConfig := action.Configuration{}
	err = actConfig.Init(env.RESTClientGetter(), namespace, "", nil)
	if err != nil {
		return fmt.Errorf("unable to initialize helm configuration: %w", err)
	}

	act := action.NewInstall(&actConfig)
	act.Namespace = namespace
	act.ReleaseName = releaseName
	act.Wait = false
	if _, err := act.RunWithContext(ctx, chart, values); err != nil {
		return fmt.Errorf("unable to install chart: %w", err)
	}

	return nil
}
