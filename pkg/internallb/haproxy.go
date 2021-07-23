package internallb

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"html/template"
)

//go:embed haproxy.cfg
var haproxyCfg string
var haproxyConfigTmpl = template.Must(template.New("").Parse(haproxyCfg))

//go:embed haproxy.yaml
var haproxyManifest string
var haproxyManifestTmpl = template.Must(template.New("").Parse(haproxyManifest))

func GenerateHAProxyConfig(loadBalancerPort int, primaries ...string) ([]byte, error) {
	var buf bytes.Buffer

	data := map[string]interface{}{
		"LoadBalancerPort": loadBalancerPort,
		"Primaries":        primaries,
	}
	err := haproxyConfigTmpl.Execute(&buf, data)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func GenerateHAProxyManifest(loadBalancerPort int, primaries ...string) ([]byte, error) {
	config, err := GenerateHAProxyConfig(loadBalancerPort, primaries...)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(config)
	data := map[string]string{
		"ConfigHash": fmt.Sprintf("%x", sum)[0:7],
	}

	var buf bytes.Buffer

	err = haproxyManifestTmpl.Execute(&buf, data)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
