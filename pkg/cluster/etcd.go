package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"net"
	"path/filepath"
	"strconv"
	"time"

	"github.com/pkg/errors"
	clientv3 "go.etcd.io/etcd/client/v3"
	kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
)

func (c *Controller) removeEtcdPeer(ip string, remainingIPs []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	removedPeerURL := getEtcdPeerURL(ip)
	etcdTLS, err := getEtcdTLS(ctx, c.Config.CertificatesDir)
	if err != nil {
		return errors.Wrap(err, "get etcd ca")
	}

	var goodEtcdEndpoints []string
	for _, ip := range remainingIPs {
		endpoint := getEtcdClientURL(ip)
		goodEtcdEndpoints = append(goodEtcdEndpoints, endpoint)
	}
	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints: goodEtcdEndpoints,
		TLS:       etcdTLS,
	})
	if err != nil {
		return errors.Wrap(err, "new etcd client")
	}
	resp, err := etcdClient.MemberList(ctx)
	if err != nil {
		return errors.Wrap(err, "list etcd members")
	}
	var purgedMemberID uint64
	for _, member := range resp.Members {
		if member.GetPeerURLs()[0] == removedPeerURL {
			purgedMemberID = member.GetID()
		}
	}
	if purgedMemberID != 0 {
		_, err = etcdClient.MemberRemove(ctx, purgedMemberID)
		if err != nil {
			return errors.Wrapf(err, "remove etcd member %d", purgedMemberID)
		}
		c.Log.Infof("Removed etcd member %d", purgedMemberID)
	} else {
		c.Log.Infof("Etcd cluster does not have member %s", removedPeerURL)
	}

	return nil
}

func getEtcdTLS(ctx context.Context, pkiDir string) (*tls.Config, error) {
	config := &tls.Config{}

	caCertPEM, err := ioutil.ReadFile(filepath.Join(pkiDir, "etcd/ca.crt"))
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	ok := pool.AppendCertsFromPEM(caCertPEM)
	if !ok {
		return nil, errors.New("failed to append CA from pem")
	}
	config.RootCAs = pool

	clientCertPEM, err := ioutil.ReadFile(filepath.Join(pkiDir, "etcd/healthcheck-client.crt"))
	if err != nil {
		return nil, err
	}

	clientKeyPEM, err := ioutil.ReadFile(filepath.Join(pkiDir, "etcd/healthcheck-client.key"))
	if err != nil {
		return nil, err
	}

	clientCert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		return nil, err
	}
	config.Certificates = append(config.Certificates, clientCert)

	return config, nil
}

func getEtcdPeerURL(ip string) string {
	return "https://" + net.JoinHostPort(ip, strconv.Itoa(kubeadmconstants.EtcdListenPeerPort))
}

func getEtcdClientURL(ip string) string {
	return "https://" + net.JoinHostPort(ip, strconv.Itoa(kubeadmconstants.EtcdListenClientPort))
}
