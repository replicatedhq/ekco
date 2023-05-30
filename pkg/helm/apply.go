package helm

import (
	"context"
	"fmt"
	"io"

	"github.com/replicatedhq/ekco/pkg/helm/chartfiles"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
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

	cephClusterValues, err := rookCephClusterConfig()
	if err != nil {
		return fmt.Errorf("unable to get %s chart values: %w", chart.Name(), err)
	}

	// install chart
	if _, err := act.RunWithContext(ctx, chart, cephClusterValues); err != nil {
		return fmt.Errorf("unable to install %s chart: %w", chart.Name(), err)
	}

	return nil
}

func rookCephClusterConfig() (map[string]interface{}, error) {
	values, err := chartutil.ReadValues(chartfiles.CephClusterChartValuesFile)
	if err != nil {
		return nil, fmt.Errorf("Unable to read rook-ceph-cluster values file: %w", err)
	}
	return values.AsMap(), nil
}
