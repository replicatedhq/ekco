package cluster

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Controller) SetKubeconfigServer(ctx context.Context, node corev1.Node, server string) error {
	c.Log.Infof("Scheduling set-kubeconfig-server task on node %s", node.Name)

	admin := util.NodeIsMaster(node)
	pod := c.setKubeconfigServerPod(node.Name, server, admin)

	pod, err := c.Config.Client.CoreV1().Pods(c.Config.HostTaskNamespace).Create(pod)
	if err != nil {
		return errors.Wrapf(err, "create set-kubeconfig-server pod on node %s", node.Name)
	}

	err = c.pollForPodCompleted(ctx, c.Config.HostTaskNamespace, pod.Name)
	if err != nil {
		if err == podFailedErr {
			c.logPodResults(c.Config.HostTaskNamespace, pod.Name)
		}
		return errors.Wrapf(err, "run set-kubeconfig-server pod on node %s", node.Name)
	}

	if err := c.deletePods(SetKubeconfigServerSelector); err != nil {
		c.Log.Warnf("Failed to delete set-kubeconfig-server pods: %v", err)
	}

	c.Log.Infof("Successfully completed set-kubeconfig-server task on node %s", node.Name)

	return nil
}

func (c *Controller) setKubeconfigServerPod(nodeName string, server string, admin bool) *corev1.Pod {
	t := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "set-kubeconfig-server-",
			Namespace:    c.Config.HostTaskNamespace,
			Labels: map[string]string{
				TaskLabel: SetKubeconfigServerValue,
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": nodeName,
			},
			Tolerations: []corev1.Toleration{
				{
					Key:      PrimaryRoleLabel,
					Effect:   corev1.TaintEffectNoSchedule,
					Operator: corev1.TolerationOpExists,
				},
			},
			Containers: []corev1.Container{
				corev1.Container{
					Name:  "set-kubeconfig-server",
					Image: c.Config.HostTaskImage,
					// ImagePullPolicy: corev1.PullIfNotPresent,
					// TODO
					ImagePullPolicy: corev1.PullAlways,
					Command: []string{
						"ekco",
						"set-kubeconfig-server",
						fmt.Sprintf("--server=%s", server),
						fmt.Sprintf("--host-etc-dir=/host/etc"),
						fmt.Sprintf("--admin=%t", admin),
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "var-run-dbus",
							MountPath: "/var/run/dbus",
						},
						{
							Name:      "host-etc-dir",
							MountPath: "/host/etc",
						},
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged: &t,
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Volumes: []corev1.Volume{
				{
					Name: "var-run-dbus",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/var/run/dbus",
						},
					},
				},
				{
					Name: "host-etc-dir",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/etc",
						},
					},
				},
			},
		},
	}
}
