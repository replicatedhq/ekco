package cluster

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

var errPodFailed error = errors.New("pod failed")

// Any time this returns true it updates the last attempted timestamp
func (c *Controller) CheckRotateCertsDue(reset bool) (bool, error) {
	client := c.Config.Client.CoreV1().ConfigMaps(c.Config.RotateCertsNamespace)
	cm, err := client.Get(context.TODO(), RotateCertsValue, metav1.GetOptions{})
	if err != nil {
		if !util.IsNotFoundErr(err) {
			return false, errors.Wrapf(err, "get configmap %s/%s", c.Config.RotateCertsNamespace, RotateCertsValue)
		}

		// create the configmap the first time
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      RotateCertsValue,
				Namespace: c.Config.RotateCertsNamespace,
			},
			Data: map[string]string{
				RotateCertsLastAttempted: time.Now().Format(time.RFC3339),
			},
		}
		if _, err := client.Create(context.TODO(), cm, metav1.CreateOptions{}); err != nil {
			return false, errors.Wrapf(err, "create configmap %s/%s", c.Config.RotateCertsNamespace, RotateCertsValue)
		}
		return true, nil
	}

	timestamp := cm.Data[RotateCertsLastAttempted]
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return false, errors.Wrap(err, "parse last attempted rotation timestamp")
	}

	if time.Since(t) < c.Config.RotateCertsCheckInterval && !reset {
		return false, nil
	}

	cm.Data[RotateCertsLastAttempted] = time.Now().Format(time.RFC3339)

	if _, err := client.Update(context.TODO(), cm, metav1.UpdateOptions{}); err != nil {
		return false, errors.Wrapf(err, "update configmap %s/%s", c.Config.RotateCertsNamespace, RotateCertsValue)
	}

	return true, nil
}

// This launches a pod on each primary to mount /etc/kubernetes and rotate the certs.
// It leaves the pods up if any fail.
func (c *Controller) RotateAllCerts(ctx context.Context) error {
	if err := c.deletePods(c.Config.RotateCertsNamespace, RotateCertsSelector); err != nil {
		c.Log.Warnf("Failed to delete rotate pods: %v", err)
	}
	opts := metav1.ListOptions{
		LabelSelector: "node-role.kubernetes.io/master=",
	}
	node, err := c.Config.Client.CoreV1().Nodes().List(context.TODO(), opts)
	if err != nil {
		return errors.Wrap(err, "list primary nodes")
	}
	if len(node.Items) == 0 {
		opts = metav1.ListOptions{
			LabelSelector: "node-role.kubernetes.io/control-plane=",
		}
		node, err = c.Config.Client.CoreV1().Nodes().List(context.TODO(), opts)
		if err != nil {
			return errors.Wrap(err, "list primary nodes")
		}
	}
	for i, node := range node.Items {
		c.Log.Debugf("Running certificate rotation task on node %s", node.Name)
		if i != 0 {
			// Sine the api server on the previous node may have restarted, give load balancers a
			// chance to detect it is healthy again before possibly restarting the api server on
			// this node
			time.Sleep(time.Second * 5)
		}
		pod := c.getRotateCertsPodConfig(node.Name)
		pod, err := c.Config.Client.CoreV1().Pods(c.Config.RotateCertsNamespace).Create(context.TODO(), pod, metav1.CreateOptions{})
		if err != nil {
			return errors.Wrapf(err, "create rotate pod for node %s", node.Name)
		}
		err = c.pollForPodCompleted(ctx, c.Config.RotateCertsNamespace, pod.Name)
		if err != nil {
			if err == errPodFailed {
				c.logPodResults(c.Config.RotateCertsNamespace, pod.Name)
			}
			return errors.Wrapf(err, "rotate certs pod for node %s", node.Name)
		}
		c.logPodResults(c.Config.RotateCertsNamespace, pod.Name)
	}

	if err := c.deletePods(c.Config.RotateCertsNamespace, RotateCertsSelector); err != nil {
		c.Log.Warnf("Failed to delete rotate pods: %v", err)
	}

	return nil
}

func (c *Controller) deletePods(namespace string, selector labels.Selector) error {
	options := metav1.ListOptions{
		LabelSelector: selector.String(),
	}

	return c.Config.Client.CoreV1().Pods(namespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, options)
}

func (c *Controller) getRotateCertsPodConfig(nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "rotate-certs-",
			Namespace:    c.Config.RotateCertsNamespace,
			Labels: map[string]string{
				RotateCertsLabel: RotateCertsValue,
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": nodeName,
			},
			Tolerations: []corev1.Toleration{
				{
					Key:      "node-role.kubernetes.io/control-plane",
					Effect:   corev1.TaintEffectNoSchedule,
					Operator: corev1.TolerationOpExists,
				},
				{
					Key:      "node-role.kubernetes.io/master",
					Effect:   corev1.TaintEffectNoSchedule,
					Operator: corev1.TolerationOpExists,
				},
			},
			Containers: []corev1.Container{
				{
					Name:            "rotate-certs",
					Image:           c.Config.RotateCertsImage,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command: []string{
						"ekco",
						"rotate-certs",
						fmt.Sprintf("--ttl=%s", c.Config.RotateCertsTTL),
					},
					Env: []corev1.EnvVar{
						{
							Name: "HOSTNAME",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "spec.nodeName",
								},
							},
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "etc-kubernetes",
							MountPath: DefaultEtcKubernetesDir,
						},
					},
					WorkingDir: DefaultEtcKubernetesDir,
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Volumes: []corev1.Volume{
				{
					Name: "etc-kubernetes",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: DefaultEtcKubernetesDir,
						},
					},
				},
			},
		},
	}
}

// Polling is resilient to restarts of the K8s API server, which is expected when rotating certs
func (c *Controller) pollForPodCompleted(ctx context.Context, namespace, name string) error {
	ticker := time.NewTicker(time.Second * 2)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			pod, err := c.Config.Client.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
			if err != nil {
				c.Log.Debugf("Poll for pod completed: get pod %s: %v", name, err)
				continue
			}
			if pod.Status.Phase == corev1.PodSucceeded {
				return nil
			}
			if pod.Status.Phase == corev1.PodFailed {
				return errPodFailed
			}
		}
	}
}

func (c *Controller) logPodResults(namespace, name string) {
	req := c.Config.Client.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{})
	logs, err := req.Stream(context.TODO())
	if err != nil {
		c.Log.Warnf("Failed to get pod %s logs: %v", name, err)
		return
	}
	defer logs.Close()

	scanner := bufio.NewScanner(logs)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\n")
		if strings.HasPrefix(line, "Error") {
			c.Log.Error(line)
		} else if strings.HasPrefix(line, "Rotated") || strings.HasPrefix(line, "Restarting") {
			c.Log.Info(line)
		} else {
			c.Log.Debug(line)
		}
	}
}
