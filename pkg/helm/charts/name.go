package charts

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed *
var chartfs embed.FS

func LatestChartByName(name string) (fs.File, string, error) {
	files, err := chartfs.ReadDir(".")
	if err != nil {
		return nil, "", fmt.Errorf("failed to read chartfiles: %w", err)
	}

	var chartOptions []string
	for _, file := range files {
		if strings.HasPrefix(file.Name(), name) {
			chartOptions = append(chartOptions, file.Name())
		}
	}

	if len(chartOptions) == 0 {
		return nil, "", fmt.Errorf("no chartfiles found for %s", name)
	}

	// sort chartOptions to find the latest version
	sort.Slice(chartOptions, func(i, j int) bool {
		return chartOptions[i] > chartOptions[j]
	})

	file, err := chartfs.Open(chartOptions[0])
	if err != nil {
		return nil, "", fmt.Errorf("failed to open chartfile %s: %w", chartOptions[0], err)
	}

	return file, chartOptions[0], nil
}
