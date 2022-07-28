package internallb

import (
	"bytes"
	_ "embed"
	"errors"
	"html/template"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// GenerateHAProxyManifest writes the generated manifest to the file only if it does not exist or
// the fileversion has changed. This avoids a few seconds of downtime when the pod is restarted
// unnecessarily.
func GenerateHAProxyManifest(filename, image string) error {
	// When making changes to the manifest file that need to be synchronized, increment the
	// fileversion number here.
	fileversion := 0

	current, err := ioutil.ReadFile(filename)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else {
		currentFileversion := getFileversion(current)
		if currentFileversion == fileversion {
			return nil
		}
	}

	manifest, err := generateHAProxyManifest(image, fileversion)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filename, manifest, 0644); err != nil {
		return err
	}

	return nil
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

var fileversionRegexp = regexp.MustCompile(`(?m)^ +fileversion: "(\d+)"`)

func getFileversion(contents []byte) int {
	matches := fileversionRegexp.FindSubmatch(contents)
	if len(matches) == 2 {
		i, _ := strconv.Atoi(string(matches[1]))
		return i
	}
	return 0
}
