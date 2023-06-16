package helm

import (
	"context"
	"fmt"
	"io"

	"github.com/replicatedhq/ekco/pkg/helm/charts/rookcephcluster"
	"go.uber.org/zap"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
)

type HelmManager struct {
	ctx          context.Context
	actionConfig *action.Configuration
	logger       *zap.SugaredLogger
}

// NewHelmManager initializes a new HelmManager instance
func NewHelmManager(ctx context.Context, namespace string, logger *zap.SugaredLogger) (*HelmManager, error) {

	// Check if the namespace name is valid
	if namespace == "" {
		return nil, fmt.Errorf("error initializing Helm Manager: namespace must be set")
	}

	// Create a Helm client from the environment
	env := cli.New()
	env.SetNamespace(namespace)

	// Create Helm Configuration
	actConfig := action.Configuration{}
	if err := actConfig.Init(env.RESTClientGetter(), namespace, "", logger.Debugf); err != nil {
		return nil, fmt.Errorf("failed to initialize helm configuration: %w", err)
	}

	return &HelmManager{ctx: ctx, actionConfig: &actConfig, logger: logger}, nil
}

// InstallChartArchive installs a packaged Helm chart (tgz) to a namespace with given values and release name
func (hm *HelmManager) InstallChartArchive(chartBytes io.Reader, values map[string]interface{}, releaseName string, namespace string) error {
	chart, err := loader.LoadArchive(chartBytes)
	if err != nil {
		return err
	}

	relName := chart.Name()
	if releaseName != "" {
		relName = releaseName
	}

	// check if CephCluster CR was already installed
	installed, err := hm.GetRelease(relName)
	if err != nil {
		return fmt.Errorf("failed to check if release %s is installed: %w", relName, err)
	}

	if installed != nil {
		hm.logger.Infof("rook-ceph-cluster already installed with release name: %s", installed.Name)
		return nil
	}

	act := action.NewInstall(hm.actionConfig)
	act.Namespace = namespace
	act.ReleaseName = relName
	act.Wait = false

	chartValues, err := rookcephcluster.ValuesMap()
	if err != nil {
		return fmt.Errorf("failed to get %s chart values: %w", chart.Name(), err)
	}

	// install chart
	if _, err := act.RunWithContext(hm.ctx, chart, chartValues); err != nil {
		return fmt.Errorf("failed to install %s chart: %w", chart.Name(), err)
	}

	return nil
}

// GetRelease returns the Helm release for a release name
func (hm *HelmManager) GetRelease(releaseName string) (*release.Release, error) {
	actList := action.NewList(hm.actionConfig)
	actList.StateMask = action.ListDeployed
	releases, err := actList.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to list installed releases: %w", err)
	}

	// no releases in the cluster
	if len(releases) == 0 {
		return nil, nil
	}

	for _, release := range releases {
		if release.Name == releaseName {
			return release, nil
		}
	}

	//if we get here we couldn't find the release
	return nil, nil
}
