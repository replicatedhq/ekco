package chartfiles

import "embed"

//go:embed *.tgz
var FS embed.FS

//go:embed values.yaml
var CephClusterChartValuesFile []byte
