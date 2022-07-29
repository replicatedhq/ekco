package internallb

import (
	_ "embed"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateHAProxyConfig(t *testing.T) {
	primaries := []string{
		"10.128.0.3",
		"10.128.0.4",
	}
	out, err := GenerateHAProxyConfig(primaries...)
	assert.NoError(t, err)

	expect := `# /etc/haproxy/haproxy.cfg
#---------------------------------------------------------------------
# Global settings
#---------------------------------------------------------------------
global
    log /dev/log local0
    log /dev/log local1 notice
    daemon

#---------------------------------------------------------------------
# common defaults that all the 'listen' and 'backend' sections will
# use if not designated in their block
# borrowed from https://github.com/openshift/machine-config-operator/blob/9674df13d76e3832c5b8404083fb6218a09503f8/templates/master/00-master/on-prem/files/haproxy-haproxy.yaml
#---------------------------------------------------------------------
defaults
    maxconn 20000
    mode    tcp
    option  dontlognull
    retries 3
    timeout http-request 30s
    timeout queue        1m
    timeout connect      10s
    timeout client       86400s
    timeout server       86400s
    timeout tunnel       86400s

frontend kubernetes-api
    bind 0.0.0.0:6444 v4v6
    option tcplog
    default_backend kubernetes-primaries

backend kubernetes-primaries
    option  httpchk GET /readyz HTTP/1.0
    option  log-health-checks
    balance roundrobin
    server k8s-primary-0 10.128.0.3:6443 weight 1 verify none check check-ssl inter 1s fall 2 rise 3
    server k8s-primary-1 10.128.0.4:6443 weight 1 verify none check check-ssl inter 1s fall 2 rise 3
`

	assert.Equal(t, expect, string(out))
}

func TestGenerateHAProxyManifest(t *testing.T) {
	out, err := generateHAProxyManifest("haproxy:1.1.1", 0)
	assert.NoError(t, err)

	expect := `apiVersion: v1
kind: Pod
metadata:
  name: haproxy
  namespace: kube-system
  labels:
    app: kurl-haproxy
    fileversion: "0"
spec:
  containers:
  - image: "haproxy:1.1.1"
    name: haproxy
    livenessProbe:
      failureThreshold: 8
      httpGet:
        host: localhost
        path: /healthz
        port: 6444
        scheme: HTTPS
    volumeMounts:
    - mountPath: /usr/local/etc/haproxy/haproxy.cfg
      name: haproxyconf
      readOnly: true
  hostNetwork: true
  volumes:
  - hostPath:
      path: /etc/haproxy/haproxy.cfg
      type: FileOrCreate
    name: haproxyconf
status: {}
`
	assert.Equal(t, expect, string(out))
}

func Test_getFileversion(t *testing.T) {
	tests := []struct {
		name     string
		contents []byte
		want     int
	}{
		{
			name: "missing",
			contents: []byte(`apiVersion: v1
kind: Pod
metadata:
  name: haproxy
  namespace: kube-system
spec:`),
			want: 0,
		},
		{
			name: "zero",
			contents: []byte(`apiVersion: v1
kind: Pod
metadata:
  name: haproxy
  namespace: kube-system
  labels:
    app: kurl-haproxy
    # when making changes to the file that need to be synchronized, increment
    # fileversion number
    fileversion: "0"
spec:`),
			want: 0,
		},
		{
			name: "one",
			contents: []byte(`apiVersion: v1
kind: Pod
metadata:
  name: haproxy
  namespace: kube-system
  labels:
    app: kurl-haproxy
    # when making changes to the file that need to be synchronized, increment
    # fileversion number
    fileversion: "1"
spec:`),
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getFileversion(tt.contents); got != tt.want {
				t.Errorf("getFileversion() = %v, want %v", got, tt.want)
			}
		})
	}
}
