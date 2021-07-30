package kubeconfig

import (
	"io/ioutil"
	"os"

	"k8s.io/client-go/tools/clientcmd"
)

func SetServer(file string, server string) error {
	f, err := os.OpenFile(file, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	bs, err := ioutil.ReadAll(f)
	if err != nil {
		return err
	}

	config, err := clientcmd.Load(bs)
	if err != nil {
		return err
	}

	for key := range config.Clusters {
		config.Clusters[key].Server = server
	}

	updated, err := clientcmd.Write(*config)
	if err != nil {
		return err
	}

	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}

	if _, err := f.Write(updated); err != nil {
		return err
	}

	return nil
}
