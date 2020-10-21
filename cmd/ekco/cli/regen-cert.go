package cli

import (
	"fmt"
	"net"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	certutil "k8s.io/client-go/util/cert"
	certsphase "k8s.io/kubernetes/cmd/kubeadm/app/phases/certs"
	"k8s.io/kubernetes/cmd/kubeadm/app/phases/certs/renewal"
	"k8s.io/kubernetes/cmd/kubeadm/app/util/pkiutil"
)

func RegenCertCmd(v *viper.Viper) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "regen-cert",
		Short: "Regenerate API server certificate",
		Long:  `Regenerate an API server certificate with different subject alternative names`,
		Args:  cobra.ExactArgs(0),
		PreRun: func(cmd *cobra.Command, args []string) {
			v.BindPFlags(cmd.Flags())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			addAltNames := v.GetString("add-alt-names")
			dropAltNames := v.GetString("drop-alt-names")
			certPath := "/etc/kubernetes/pki/apiserver.crt"

			certs, err := certutil.CertsFromFile(certPath)
			if err != nil {
				return errors.Wrapf(err, "parse cert from %s", certPath)
			}
			if len(certs) != 1 {
				return fmt.Errorf("Expected exactly 1 cert in %s, got %d", certPath, len(certs))
			}
			cert := certs[0]
			cfg := &certutil.Config{
				CommonName:   cert.Subject.CommonName,
				Organization: cert.Subject.Organization,
				AltNames:     certutil.AltNames{},
				Usages:       cert.ExtKeyUsage,
			}

			newCertIPs := map[string]net.IP{}
			newCertDNSNames := map[string]string{}

			dropIPs := map[string]net.IP{}
			dropDNSNames := map[string]string{}

			for _, dropAltName := range strings.Split(dropAltNames, ",") {
				ip := net.ParseIP(dropAltName)
				if ip != nil {
					dropIPs[ip.String()] = ip
					continue
				}
				dropDNSNames[dropAltName] = dropAltName
			}

			for _, oldCertIP := range cert.IPAddresses {
				if _, drop := dropIPs[oldCertIP.String()]; drop {
					fmt.Printf("Drop IP from old cert: %s\n", oldCertIP)
					continue
				}
				fmt.Printf("Include IP from old cert: %s\n", oldCertIP)
				newCertIPs[oldCertIP.String()] = oldCertIP
			}
			for _, oldCertDNSName := range cert.DNSNames {
				if oldCertDNSName == "" {
					continue
				}
				if _, drop := dropDNSNames[oldCertDNSName]; drop {
					fmt.Printf("Drop DNS name from old cert: %s\n", oldCertDNSName)
					continue
				}
				fmt.Printf("Include DNS name from old cert: %s\n", oldCertDNSName)
				newCertDNSNames[oldCertDNSName] = oldCertDNSName
			}

			for _, addAltName := range strings.Split(addAltNames, ",") {
				if addAltName == "" {
					continue
				}
				ip := net.ParseIP(addAltName)
				if ip != nil {
					if _, ok := newCertIPs[ip.String()]; ok {
						continue
					}
					fmt.Printf("Add new IP: %s\n", ip)
					newCertIPs[ip.String()] = ip
					continue
				}
				if _, ok := newCertDNSNames[ip.String()]; ok {
					continue
				}
				fmt.Printf("Add new DNS name: %s\n", addAltName)
				newCertDNSNames[addAltName] = addAltName
			}

			for _, ip := range newCertIPs {
				cfg.AltNames.IPs = append(cfg.AltNames.IPs, ip)
			}
			for _, dnsName := range newCertDNSNames {
				cfg.AltNames.DNSNames = append(cfg.AltNames.DNSNames, dnsName)
			}

			caCert, caKey, err := certsphase.LoadCertificateAuthority("/etc/kubernetes/pki", "ca")
			if err != nil {
				return errors.Wrap(err, "parse CA from /etc/kubernets/pki")
			}

			newCert, newKey, err := renewal.NewFileRenewer(caCert, caKey).Renew(cfg)
			if err != nil {
				return errors.Wrapf(err, "regenerate %s", certPath)
			}

			if err := pkiutil.WriteCertAndKey("/etc/kubernetes/pki", "apiserver", newCert, newKey); err != nil {
				return errors.Wrap(err, "write new cert to /etc/kubernetes/pki/apiserver.crt")
			}

			return nil
		},
	}

	cmd.Flags().String("add-alt-names", "", "Comma-separated list of subject alternative names to add to the new certificate")
	cmd.Flags().String("drop-alt-names", "", "Comma-separated list of subject alternative names to drop from the new certificate")

	return cmd
}
