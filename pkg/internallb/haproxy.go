package internallb

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io/ioutil"
	"os"
	"path/filepath"
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

// GenerateHAProxyManifest writes the generated manifest to the file only if it does not have the
// correct hash. This avoids a few seconds of downtime when the pod is restarted unnecessarily.
func GenerateHAProxyManifest(filename string, primaries ...string) error {
	config, err := GenerateHAProxyConfig(primaries...)
	if err != nil {
		return err
	}

	sum := sha256.Sum256(config)
	hash := fmt.Sprintf("%x", sum)[0:7]

	current, err := ioutil.ReadFile(filename)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		if bytes.Contains(current, []byte(hash)) {
			return nil
		}
	}

	manifest, err := generateHAProxyManifest(hash)

	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filename, manifest, 0644); err != nil {
		return err
	}

	return nil
}

func generateHAProxyManifest(configHash string) ([]byte, error) {
	var buf bytes.Buffer
	data := map[string]string{
		"ConfigHash": configHash,
	}

	err := haproxyManifestTmpl.Execute(&buf, data)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
