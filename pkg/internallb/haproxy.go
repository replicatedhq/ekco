package internallb

import (
	"bytes"
	_ "embed"
	"errors"
	"html/template"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

//go:embed haproxy.cfg
var haproxyCfg string
var haproxyConfigTmpl = template.Must(template.New("").Parse(haproxyCfg))

//go:embed haproxy.yaml
var haproxyManifest string
var haproxyManifestTmpl = template.Must(template.New("").Parse(haproxyManifest))

func GenerateHAProxyConfig(primaries ...string) ([]byte, error) {
	var buf bytes.Buffer

	data := map[string]interface{}{
		"Primaries": primaries,
	}
	err := haproxyConfigTmpl.Execute(&buf, data)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// DefaultFileversion should be incremented When making changes to the manifest file that need to
// be synchronized.
const DefaultFileversion = 0

// GenerateHAProxyManifest writes the generated manifest to the file only if it does not exist or
// the fileversion has changed. This avoids a few seconds of downtime when the pod is restarted
// unnecessarily.
func GenerateHAProxyManifest(filename, image string, fileversion int) (bool, error) {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return false, err
	}

	current, err := ioutil.ReadFile(filename)
	if err == nil {
		pod := corev1.Pod{}
		if err := yaml.Unmarshal(current, &pod); err != nil {
			return false, err
		}

		currentFileversion := getFileversion(pod)
		currentImage := getImage(pod)
		if currentFileversion == fileversion && currentImage == image {
			return false, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}

	manifest, err := generateHAProxyManifest(image, fileversion)
	if err != nil {
		return false, err
	}

	if err := os.WriteFile(filename, manifest, 0644); err != nil {
		return false, err
	}

	return true, nil
}

func generateHAProxyManifest(image string, fileversion int) ([]byte, error) {
	var buf bytes.Buffer
	data := map[string]string{
		"Image":       image,
		"Fileversion": strconv.Itoa(fileversion),
	}

	err := haproxyManifestTmpl.Execute(&buf, data)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

const HAProxyFileversionAnnotation = "kurl.sh/haproxy-fileversion"

func getFileversion(pod corev1.Pod) int {
	annotations := pod.GetAnnotations()
	str := annotations[HAProxyFileversionAnnotation]
	if str == "" {
		return 0
	}
	i, _ := strconv.Atoi(str)
	return i
}

func getImage(pod corev1.Pod) string {
	if len(pod.Spec.Containers) == 0 {
		return ""
	}
	return pod.Spec.Containers[0].Image
}
