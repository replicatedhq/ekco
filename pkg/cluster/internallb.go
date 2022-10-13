package cluster

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/replicatedhq/ekco/pkg/k8s"
	"github.com/replicatedhq/ekco/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func (c *Controller) ReconcileInternalLB(ctx context.Context, nodes []corev1.Node) error {
	var nodeProps []string
	for _, node := range nodes {
		nodeProps = append(nodeProps, fmt.Sprintf("%s %t %s", node.Name, util.NodeIsMaster(node), util.NodeInternalIP(node)))
	}
	sort.Strings(nodeProps)
	nextInternalLB := strings.Join(nodeProps, ",")

	client := c.Config.Client.CoreV1().ConfigMaps(c.Config.HostTaskNamespace)
	cm, err := client.Get(context.TODO(), UpdateInternalLBValue, metav1.GetOptions{})
	if err != nil {
		if !util.IsNotFoundErr(err) {
			return errors.Wrapf(err, "get configmap %s/%s", c.Config.HostTaskNamespace, UpdateInternalLBValue)
		}

		// create the configmap the first time
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      UpdateInternalLBValue,
				Namespace: c.Config.HostTaskNamespace,
			},
			Data: map[string]string{
				UpdateInternalLBValue: "", // hasn't yet been successful
			},
		}
		if _, err := client.Create(context.TODO(), cm, metav1.CreateOptions{}); err != nil {
			return errors.Wrapf(err, "create configmap %s/%s", c.Config.HostTaskNamespace, UpdateInternalLBValue)
		}
		return nil
	}

	if nextInternalLB == cm.Data[UpdateInternalLBValue] {
		return nil
	}

	if err := c.UpdateInternalLB(ctx, nodes); err != nil {
		return err
	}
	cm.Data[UpdateInternalLBValue] = nextInternalLB

	if _, err := client.Update(context.TODO(), cm, metav1.UpdateOptions{}); err != nil {
		return errors.Wrapf(err, "update configmap %s/%s", c.Config.HostTaskNamespace, UpdateInternalLBValue)
	}
	return nil
}

// Update /etc/haproxy/haproxy.cfg and /etc/kubernetes/manifests/haproxy.yaml on all nodes.
func (c *Controller) UpdateInternalLB(ctx context.Context, nodes []corev1.Node) error {
	if err := c.deletePods(c.Config.HostTaskNamespace, UpdateInternalLBSelector); err != nil {
		c.Log.Warnf("Failed to delete update internal loadbalancer pods: %v", err)
	}
	var primaryHosts []string

	for _, node := range nodes {
		if !util.NodeIsMaster(node) {
			continue
		}
		if host := util.NodeInternalIP(node); host != "" {
			primaryHosts = append(primaryHosts, host)
		}
	}

	if len(primaryHosts) == 0 {
		c.Log.Warn("Skipping update of internal loadbalancer: no primary hosts found")
		return nil
	}
	c.Log.Info("Running internal loadbalancer update task on all nodes")

	for _, node := range nodes {
		c.Log.Debugf("Running internal loadbalancer update task on node %s", node.Name)

		pod := c.getUpdateInternalLBPod(node.Name, primaryHosts...)

		pod, err := c.Config.Client.CoreV1().Pods(c.Config.HostTaskNamespace).Create(context.TODO(), pod, metav1.CreateOptions{})
		if err != nil {
			return errors.Wrapf(err, "create update internal loadbalancer pod for node %s", node.Name)
		}

		err = c.pollForPodCompleted(ctx, c.Config.HostTaskNamespace, pod.Name)
		if err != nil {
			if err == errPodFailed {
				c.logPodResults(c.Config.HostTaskNamespace, pod.Name)
			}
			return errors.Wrapf(err, "update internal loadbalancer pod for node %s", node.Name)
		}
	}

	if err := c.deletePods(c.Config.HostTaskNamespace, UpdateInternalLBSelector); err != nil {
		c.Log.Warnf("Failed to delete internal loadbalancer update pods: %v", err)
	}

	if err := c.sighupPods("kube-system", labels.SelectorFromSet(labels.Set{"app": "kurl-haproxy"}), "haproxy"); err != nil {
		c.Log.Warnf("Failed to send SIGHUP to haproxy pods: %v", err)
		return nil
	}

	c.Log.Info("Successfully completed internal loadbalancer update task on all nodes")

	return nil
}

func (c *Controller) sighupPods(namespace string, selector labels.Selector, container string) error {
	options := metav1.ListOptions{
		LabelSelector: selector.String(),
	}

	pods, err := c.Config.Client.CoreV1().Pods(namespace).List(context.TODO(), options)
	if err != nil {
		return errors.Wrap(err, "list pods")
	}
	if len(pods.Items) == 0 {
		return errors.New("found no pods")
	}

	cmd := []string{"/bin/kill", "-HUP", "1"}

	errs := make(chan error, len(pods.Items))

	wg := sync.WaitGroup{}
	wg.Add(len(pods.Items))
	go func() {
		wg.Wait()
		close(errs)
	}()

	for _, pod := range pods.Items {
		pod := pod
		go func() {
			defer wg.Done()
			exitCode, _, stderr, err := k8s.SyncExec(c.Config.Client.CoreV1(), c.Config.ClientConfig, namespace, pod.Name, container, cmd...)
			if err != nil {
				errs <- errors.Wrapf(err, "exec pod %s", pod.Name)
			} else if exitCode != 0 {
				errs <- errors.Errorf("exec pod %s: exitcode=%d, stderr=%q", pod.Name, exitCode, stderr)
			} else {
				errs <- nil
			}
		}()
	}

	timeout := time.After(time.Minute)
	for {
		select {
		case <-timeout:
			return errors.New("timeout")
		case err, ok := <-errs:
			if !ok {
				// all returned with no errors
				return nil
			}
			if err != nil {
				// short circuit on first error
				return err
			}
		}
	}
}

func (c *Controller) getUpdateInternalLBPod(nodeName string, primaries ...string) *corev1.Pod {
	namespace := c.Config.HostTaskNamespace
	image := c.Config.HostTaskImage
	haproxyImage := c.Config.InternalLoadBalancerHAProxyImage

	hosts := strings.Join(primaries, ",")

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "update-haproxy-",
			Namespace:    namespace,
			Labels: map[string]string{
				TaskLabel: UpdateInternalLBValue,
			},
		},
		Spec: corev1.PodSpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": nodeName,
			},
			Tolerations: []corev1.Toleration{
				{
					Key:      "node-role.kubernetes.io/master",
					Effect:   corev1.TaintEffectNoSchedule,
					Operator: corev1.TolerationOpExists,
				},
				{
					Key:      "node-role.kubernetes.io/control-plane",
					Effect:   corev1.TaintEffectNoSchedule,
					Operator: corev1.TolerationOpExists,
				},
			},
			InitContainers: []corev1.Container{
				{
					Name:            "config",
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command: []string{
						"/bin/bash",
						"-c",
						fmt.Sprintf("mkdir -p /host/etc/haproxy && /usr/bin/ekco generate-haproxy-config --primary-host=%s > /host/etc/haproxy/haproxy.cfg", hosts),
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "etc",
							MountPath: "/host/etc",
						},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name:            "manifest",
					Image:           image,
					ImagePullPolicy: corev1.PullIfNotPresent,
					Command: []string{
						"/bin/bash",
						"-c",
						fmt.Sprintf("/usr/bin/ekco generate-haproxy-manifest --primary-host=%s --file /host/etc/kubernetes/manifests/haproxy.yaml --image=%s", hosts, haproxyImage),
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "etc",
							MountPath: "/host/etc",
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
			Volumes: []corev1.Volume{
				{
					Name: "etc",
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
