package kubeconfig

import (
	"context"

	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/pkg/errors"
)

func RestartKubelet(ctx context.Context) error {
	conn, err := dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to connect to dbus")
	}
	defer conn.Close()

	_, err = conn.RestartUnitContext(ctx, "kubelet.service", "replace", nil)
	if err != nil {
		return err
	}

	return nil
}
