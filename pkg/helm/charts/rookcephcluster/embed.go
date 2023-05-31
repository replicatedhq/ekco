package rookcephcluster

import (
	"embed"
	"fmt"
)

//go:embed *
var FS embed.FS

// ValuesYAML returns the values.yaml file as a byte array
func valuesYAML() ([]byte, error) {
	return FS.ReadFile("values.yaml")
}

// ValuesMap return the values.yaml files as a Map
func ValuesMap() (map[string]interface{}, error) {
	valuesData, err := valuesYAML()
	if err != nil {
		return nil, fmt.Errorf("Unable to read embedded rook-ceph-cluster values file: %w", err)
	}
	return map[string]interface{}{"values.yaml": string(valuesData)}, nil
}
