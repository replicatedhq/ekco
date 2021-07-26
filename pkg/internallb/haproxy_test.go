package internallb

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateHAProxyConfig(t *testing.T) {
	primaries := []string{
		"10.128.0.3",
		"10.128.0.4",
	}
	out, err := GenerateHAProxyConfig(6444, primaries...)
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
#---------------------------------------------------------------------
defaults
    mode                    http
    log                     global
    option                  httplog
    option                  dontlognull
    option http-server-close
    option forwardfor       except 127.0.0.0/8
    option                  redispatch
    retries                 1
    timeout http-request    10s
    timeout queue           20s
    timeout connect         5s
    timeout client          20s
    timeout server          20s
    timeout http-keep-alive 10s
    timeout check           10s

frontend kubernetes-api
    bind 0.0.0.0:6444
    mode tcp
    option tcplog
    default_backend kubernetes-primaries

backend kubernetes-primaries
    option httpchk GET /healthz
    http-check expect status 200
    mode tcp
    option ssl-hello-chk
    balance roundrobin
        server k8s-primary-0 10.128.0.3:6443 check fall 3 rise 2
        server k8s-primary-1 10.128.0.4:6443 check fall 3 rise 2
`

	assert.Equal(t, expect, string(out))
}

func TestGenerateHAProxyManifest(t *testing.T) {
	out, err := generateHAProxyManifest("5b02714")
	assert.NoError(t, err)

	expect := `apiVersion: v1
kind: Pod
metadata:
  name: haproxy
  namespace: kube-system
spec:
  containers:
  - image: haproxy:2.4.2
    name: haproxy
    env:
    - name: HAPROXY_CONFIG_HASH
      value: "5b02714"
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
