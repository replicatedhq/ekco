package internallb

import (
	_ "embed"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
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

func Test_generateHAProxyManifest(t *testing.T) {
	out, err := generateHAProxyManifest("haproxy:1.1.1", 0)
	assert.NoError(t, err)

	expect := `apiVersion: v1
kind: Pod
metadata:
  name: haproxy
  namespace: kube-system
  labels:
    app: kurl-haproxy
  annotations:
    kurl.sh/haproxy-fileversion: "0"
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
		name string
		pod  corev1.Pod
		want int
	}{
		{
			name: "missing",
			pod:  corev1.Pod{},
			want: 0,
		},
		{
			name: "zero",
			pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					HAProxyFileversionAnnotation: "0",
				},
			}},
			want: 0,
		},
		{
			name: "one",
			pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					HAProxyFileversionAnnotation: "1",
				},
			}},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getFileversion(tt.pod); got != tt.want {
				t.Errorf("getFileversion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getImage(t *testing.T) {
	tests := []struct {
		name string
		pod  corev1.Pod
		want string
	}{
		{
			name: "missing",
			pod:  corev1.Pod{},
			want: "",
		},
		{
			name: "haproxy:lts-alpine",
			pod: corev1.Pod{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Image: "haproxy:lts-alpine",
				}},
			}},
			want: "haproxy:lts-alpine",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getImage(tt.pod); got != tt.want {
				t.Errorf("getImage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGenerateHAProxyManifestNew(t *testing.T) {
	dir, err := os.MkdirTemp("", "TestGenerateHAProxyManifestNew")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	filename := filepath.Join(dir, "haproxy.yaml")
	image := "haproxy:lts-alpine"

	didUpdate, err := GenerateHAProxyManifest(filename, image, 0)
	require.NoError(t, err)

	require.True(t, didUpdate)

	contents, err := ioutil.ReadFile(filename)
	require.NoError(t, err)

	pod := corev1.Pod{}
	err = yaml.Unmarshal(contents, &pod)
	require.NoError(t, err)

	currentFileversion := getFileversion(pod)
	currentImage := getImage(pod)

	assert.Equal(t, 0, currentFileversion)
	assert.Equal(t, image, currentImage)
}

func TestGenerateHAProxyManifestExistingNoUpdate(t *testing.T) {
	dir, err := os.MkdirTemp("", "TestGenerateHAProxyManifestNew")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	filename := filepath.Join(dir, "haproxy.yaml")
	image := "haproxy:lts-alpine"

	_, err = GenerateHAProxyManifest(filename, image, 0)
	require.NoError(t, err)

	didUpdate, err := GenerateHAProxyManifest(filename, image, 0)
	require.NoError(t, err)

	require.False(t, didUpdate)
}

func TestGenerateHAProxyManifestNewImage(t *testing.T) {
	dir, err := os.MkdirTemp("", "TestGenerateHAProxyManifestNew")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	filename := filepath.Join(dir, "haproxy.yaml")
	image := "haproxy:lts-alpine"

	_, err = GenerateHAProxyManifest(filename, image, 0)
	require.NoError(t, err)

	image = "haproxy:2.6.2-alpine3.16"

	didUpdate, err := GenerateHAProxyManifest(filename, image, 0)
	require.NoError(t, err)

	require.True(t, didUpdate)

	contents, err := ioutil.ReadFile(filename)
	require.NoError(t, err)

	pod := corev1.Pod{}
	err = yaml.Unmarshal(contents, &pod)
	require.NoError(t, err)

	currentFileversion := getFileversion(pod)
	currentImage := getImage(pod)

	assert.Equal(t, 0, currentFileversion)
	assert.Equal(t, image, currentImage)
}

func TestGenerateHAProxyManifestNewFileversion(t *testing.T) {
	dir, err := os.MkdirTemp("", "TestGenerateHAProxyManifestNew")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	filename := filepath.Join(dir, "haproxy.yaml")
	image := "haproxy:lts-alpine"

	_, err = GenerateHAProxyManifest(filename, image, 0)
	require.NoError(t, err)

	didUpdate, err := GenerateHAProxyManifest(filename, image, 1)
	require.NoError(t, err)

	require.True(t, didUpdate)

	contents, err := ioutil.ReadFile(filename)
	require.NoError(t, err)

	pod := corev1.Pod{}
	err = yaml.Unmarshal(contents, &pod)
	require.NoError(t, err)

	currentFileversion := getFileversion(pod)
	currentImage := getImage(pod)

	assert.Equal(t, 1, currentFileversion)
	assert.Equal(t, image, currentImage)
}
