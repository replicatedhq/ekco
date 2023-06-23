package rookcephcluster

import (
	"embed"
	"fmt"

	"sigs.k8s.io/yaml"
)

//go:embed *
var FS embed.FS

// ValuesMap return the values.yaml files as a Map
func ValuesMap() (map[string]interface{}, error) {
	valuesData, err := FS.ReadFile("values.yaml")
	if err != nil {
		return nil, fmt.Errorf("Unable to read embedded rook-ceph-cluster values file: %w", err)
	}
	values := map[string]interface{}{}
	if err := yaml.Unmarshal(valuesData, &values); err != nil {
		return nil, fmt.Errorf("failed to unmarshal values file: %w", err)
	}
	return values, nil
}
