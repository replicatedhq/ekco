package k8s

const (
	// JSONPatchOpAdd constant op "add"
	JSONPatchOpAdd = "add"
	// JSONPatchOpRemove constant op "remove"
	JSONPatchOpRemove = "remove"
	// JSONPatchOpReplace constant op "replace"
	JSONPatchOpReplace = "replace"
)

// JSONPatchOperation specifies a single JSON patch operation.
type JSONPatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}
