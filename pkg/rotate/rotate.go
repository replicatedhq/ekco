package rotate

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/util/duration"
	kubeadmapi "k8s.io/kubernetes/cmd/kubeadm/app/apis/kubeadm"
	kubeadmconstants "k8s.io/kubernetes/cmd/kubeadm/app/constants"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/certs/renewal"
)

// RotateCerts is run on primary nodes in short-lived pods scheduled by the ekco operator, which
// aggregates and parses the output of this function to determine when certificates were rotated
// and components restarted so that those actions can be logged at the info level in the operator's
// logs where they are more likely to be seen.
func RotateCerts(ttl time.Duration, hostname string) error {
	confDir := "/etc/kubernetes"
	pkiDir := filepath.Join(confDir, "pki")

	cc := &kubeadmapi.ClusterConfiguration{
		CertificatesDir: pkiDir,
	}
	rm, err := renewal.NewManager(cc, confDir)
	if err != nil {
		return errors.Wrap(err, "new renewal manager")
	}

	restartControllerManager := false
	restartScheduler := false
	restartAPIServer := false

	for _, handler := range rm.Certificates() {
		if ok, err := rm.CertificateExists(handler.Name); err != nil {
			return errors.Wrapf(err, "check for existing %s on host %s", handler.Name, hostname)
		} else if !ok {
			return fmt.Errorf("Missing certificate %s on %s", handler.Name, hostname)
		}

		ei, err := rm.GetCertificateExpirationInfo(handler.Name)
		if err != nil {
			return errors.Wrapf(err, "get certificate %s expiration on host %s", handler.Name, hostname)
		}
		if ei.ResidualTime() > ttl {
			fmt.Printf("%s has %s until expiration on host %s, skipping renewal\n", handler.Name, duration.ShortHumanDuration(ei.ResidualTime()), hostname)
			continue
		}

		renewed, err := rm.RenewUsingLocalCA(handler.Name)
		if err != nil {
			return errors.Wrapf(err, "renew %s on host %s", handler.Name, hostname)
		}
		if !renewed {
			return errors.Wrapf(err, "%s has external CA %s on host %s", handler.Name, handler.CABaseName, hostname)
		}

		// Etcd reloads client and server certs from disk (no restart needed)
		// API server reloads only server certs from disk
		switch handler.Name {
		case kubeadmconstants.ControllerManagerKubeConfigFileName:
			restartControllerManager = true
		case kubeadmconstants.SchedulerKubeConfigFileName:
			restartScheduler = true
		case kubeadmconstants.APIServerEtcdClientCertAndKeyBaseName:
			restartAPIServer = true
		case kubeadmconstants.APIServerKubeletClientCertAndKeyBaseName:
			restartAPIServer = true
		case kubeadmconstants.FrontProxyClientCertAndKeyBaseName:
			restartAPIServer = true
		}

		// warning - this output is parsed by the ekco operator
		fmt.Printf("Rotated %s on host %s\n", handler.Name, hostname)
	}

	if restartControllerManager {
		if err := restartStaticPod("kube-controller-manager.yaml", hostname); err != nil {
			return err
		}
	}

	if restartScheduler {
		if err := restartStaticPod("kube-scheduler.yaml", hostname); err != nil {
			return err
		}
	}

	if restartAPIServer {
		if err := restartStaticPod("kube-apiserver.yaml", hostname); err != nil {
			return err
		}
	}

	return nil
}

func restartStaticPod(filename string, hostname string) error {
	p := filepath.Join("/etc/kubernetes/manifests", filename)
	tmp := filepath.Join("/etc/kubernetes", filename)

	// warning - this output is parsed by the ekco operator
	fmt.Printf("Restarting static pod %s on host %s\n", p, hostname)

	if err := os.Rename(p, tmp); err != nil {
		return errors.Wrapf(err, "mv %s to %s", p, tmp)
	}

	if err := os.Rename(tmp, p); err != nil {
		return errors.Wrapf(err, "mv %s to %s", tmp, p)
	}

	return nil
}
