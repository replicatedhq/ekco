package cluster

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/replicatedhq/ekco/pkg/objectstore"
	"github.com/replicatedhq/ekco/pkg/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
)

func (c *Controller) ScaleMinioStatefulset(ctx context.Context, ns string) error {
	currentScale, desiredScale, err := c.getMinioScale(ctx, ns)
	if err != nil {
		return fmt.Errorf("determine current ha-minio statefulset status: %w", err)
	}

	if currentScale == desiredScale {
		return nil // already scaled
	}

	c.Log.Infof("Scaling HA MinIO Statefulset to %d replicas", desiredScale)

	minioScale, err := c.Config.Client.AppsV1().StatefulSets(ns).GetScale(ctx, "ha-minio", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get ha-minio statefulset scale: %w", err)
	}

	minioScale.Spec.Replicas = desiredScale

	_, err = c.Config.Client.AppsV1().StatefulSets(ns).UpdateScale(ctx, "ha-minio", minioScale, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to scale ha-minio statefulset: %w", err)
	}

	c.Log.Infof("Scaled HA MinIO Statefulset to %d replicas", desiredScale)
	return nil
}

// MigrateMinioData moves data from the un-replicated minio deployment to the HA minio statefulset,
// using kurl's sync-object-store.
func (c *Controller) MigrateMinioData(ctx context.Context, utilImage string, ns string) error {
	// check if the ha-minio statefulset is ready to have data migrated to it
	// if it's not, return nil - we'll migrate on a future reconcile
	healthy := c.haMinioHealthy(ns)
	if !healthy {
		c.Log.Infof("Not migrating data to HA Minio statefulset as it is not yet healthy")
		return nil
	}

	c.Log.Infof("Migrating data to HA Minio statefulset")
	// first, get the minio service.
	// if it exists, we will delete it to prevent reads and writes during the migration.
	_, err := c.Config.Client.CoreV1().Services(ns).Get(ctx, "minio", metav1.GetOptions{})
	if err != nil {
		if !util.IsNotFoundErr(err) {
			return fmt.Errorf("get existing minio service: %w", err)
		}
	} else {
		// delete existing minio service
		c.Log.Infof("Disabling existing (non-HA) MinIO service")
		doesNotExistSelector := `
[ { "op": "replace", "path": "/spec/selector", "value": {"doesnotexist": "doesnotexist"} } ]
`
		_, err = c.Config.Client.CoreV1().Services(ns).Patch(ctx, "minio", apitypes.JSONPatchType, []byte(doesNotExistSelector), metav1.PatchOptions{})
		if err != nil {
			return fmt.Errorf("disable existing minio service: %w", err)
		}
	}

	// discover the IP address of the existing minio pod to migrate from
	podIP := ""
	minioPods, err := c.Config.Client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app=minio"})
	if err != nil {
		return fmt.Errorf("list minio pods: %w", err)
	}
	if len(minioPods.Items) == 0 {
		return fmt.Errorf("unable to find existing minio pod to migrate from")
	}
	podIP = minioPods.Items[0].Status.PodIP

	// get the minio credentials to be used for the migration
	credentialSecret, err := c.Config.Client.CoreV1().Secrets(ns).Get(ctx, "minio-credentials", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("retrieve minio credentials: %w", err)
	}

	minioAccessKey := string(credentialSecret.Data["MINIO_ACCESS_KEY"])
	minioSecretKey := string(credentialSecret.Data["MINIO_SECRET_KEY"])

	c.Log.Infof("Waiting for MinIO data to be migrated")

	err = objectstore.SyncAllBuckets(ctx, fmt.Sprintf("%s:9000", podIP), minioAccessKey, minioSecretKey, fmt.Sprintf("ha-minio.%s.svc.cluster.local", ns), minioAccessKey, minioSecretKey, c.Log.Infof)
	if err != nil {
		return fmt.Errorf("sync minio data: %w", err)
	}

	c.Log.Infof("Enabling new HA MinIO service")
	haMinioSelector := `
[ { "op": "replace", "path": "/spec/selector", "value": {"app": "ha-minio"} } ]
`
	_, err = c.Config.Client.CoreV1().Services(ns).Patch(ctx, "minio", apitypes.JSONPatchType, []byte(haMinioSelector), metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("failed to enable minio service: %w", err)
	}

	// delete the minio deployment and pvc
	err = c.Config.Client.AppsV1().Deployments(ns).Delete(ctx, "minio", metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to clean up minio deployment: %w", err)
	}
	err = c.Config.Client.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, "minio-pv-claim", metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to clean up minio pvc: %w", err)
	}

	c.Log.Infof("Successfully migrated to HA MinIO")
	return nil
}

// check that the minio service exists and is not disabled - if it is disabled point it at ha-minio
// this function should only be called if the 'minio' deployment does not exist
func (c *Controller) EnsureHAMinioSvc(ctx context.Context, ns string) error {
	currentSvc, err := c.Config.Client.CoreV1().Services(ns).Get(ctx, "minio", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get minio service: %w", err)
	}
	if currentSvc.Spec.Selector["doesnotexist"] == "doesnotexist" {
		// minio service is disabled, re-enable it
		c.Log.Infof("Enabling MinIO service")
		currentSvc.Spec.Selector = map[string]string{"app": "ha-minio"}

		_, err = c.Config.Client.CoreV1().Services(ns).Update(ctx, currentSvc, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to enable minio service: %w", err)
		}
	}

	return nil
}

// MaybeRebalanceMinioServers
// first, check if minio is healthy. If it is not healthy, don't do anything.
// (this may require manual intervention, or may be resolved when nodes come back online)
// Then, check for pods that have been unschedulable for more than 5 minutes
// and delete the underlying volume + pod.
// This will allow the pod to be scheduled on a node that still exists and for data to be rebalanced there.
// After ensuring that there are as many running replicas as possible, we can rearrange replicas to maximize the number
// of nodes that can be lost before losing data. To do this, first
// check if more than ceil(replicas/nodes) replicas exist on one node.
// If it does, see if we can reschedule one of those replicas safely.
// If such a node does not exist, instead look for nodes with less than floor(replicas/nodes) replicas,
// and if it exists reschedule a replica from a node with ceil(replicas/nodes).
func (c *Controller) MaybeRebalanceMinioServers(ctx context.Context, ns string) error {
	currentScale, _, err := c.getMinioScale(ctx, ns)
	if err != nil {
		c.Log.Infof("Unable to determine if MinIO has been scaled up: %s", err.Error())
		return nil
	}
	if currentScale == 0 {
		return nil // no pods to rebalance as there are no pods
	}

	if !c.haMinioHealthy(ns) {
		c.Log.Infof("Not rebalancing Minio pods as the statefulset is not healthy")
		return nil
	}

	minioPods, err := c.Config.Client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app=ha-minio"})
	if err != nil {
		return fmt.Errorf("faled to get ha-minio pods: %w", err)
	}

	for _, minioPod := range minioPods.Items {
		if minioPod.Status.Phase != corev1.PodPending {
			continue
		}
		for _, condition := range minioPod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
				if time.Since(condition.LastTransitionTime.Time) > time.Minute*5 {
					// pod has been unschedulable for 5 minutes, we can assume it should be rescheduled
					return c.rescheduleOnePod(ctx, minioPod)
				}
			}
		}
	}

	// make a map of nodes to minio pods
	numPods := len(minioPods.Items)
	nodeMinioPods := map[string][]corev1.Pod{}
	for _, minioPod := range minioPods.Items {
		if _, ok := nodeMinioPods[minioPod.Status.HostIP]; !ok {
			nodeMinioPods[minioPod.Status.HostIP] = []corev1.Pod{}
		}

		nodeMinioPods[minioPod.Status.HostIP] = append(nodeMinioPods[minioPod.Status.HostIP], minioPod)
	}

	// get the total number of healthy nodes
	nodes, err := c.Config.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	m, w := util.NodeReadyCounts(nodes.Items)
	readyNodes := m + w

	maxNodePods := int(math.Ceil(float64(numPods) / float64(readyNodes)))
	minNodePods := int(math.Floor(float64(numPods) / float64(readyNodes)))

	// if there are more ready nodes than nodes running minio pods, we should reschedule a minio pod from a node that has 2+ (because one has zero)
	shouldRescheduleAnyDuplicate := len(nodeMinioPods) < readyNodes
	// if there are any nodes running less than the minimum (balanced) number of minio pods, we should reschedule from a node running the maximum number of pods
	shouldRescheduleFromMax := false
	for _, nodePods := range nodeMinioPods {
		if len(nodePods) < minNodePods {
			shouldRescheduleFromMax = true
		}
	}

	for _, nodePods := range nodeMinioPods {
		if len(nodePods) > maxNodePods {
			// reschedule a pod from this node because it has more than the expected maximum
			if c.haMinioPodSafeToReschedule(nodePods[0]) {
				return c.rescheduleOnePod(ctx, nodePods[0])
			}
		}
	}
	for _, nodePods := range nodeMinioPods {
		if shouldRescheduleFromMax && len(nodePods) == maxNodePods {
			// reschedule a pod from this node because there is a node with less than the minimum number of pods and this node has the maximum
			if c.haMinioPodSafeToReschedule(nodePods[0]) {
				return c.rescheduleOnePod(ctx, nodePods[0])
			}
		}
	}
	for _, nodePods := range nodeMinioPods {
		if shouldRescheduleAnyDuplicate && len(nodePods) > 1 {
			// reschedule a pod from this node because there is a node with zero minio pods
			if c.haMinioPodSafeToReschedule(nodePods[0]) {
				return c.rescheduleOnePod(ctx, nodePods[0])
			}
		}
	}

	return nil
}

func (c *Controller) DoesHAMinioExist(ctx context.Context, ns string) (bool, error) {
	_, _, err := c.getMinioScale(ctx, ns)
	if err != nil {
		if util.IsNotFoundErr(err) {
			return false, nil
		}

		return false, err
	}

	return true, nil
}

func (c *Controller) rescheduleOnePod(ctx context.Context, pod corev1.Pod) error {
	ns := pod.Namespace

	// determine what PVC this pod is using, and delete that PVC - and then delete the pod
	claimName := ""
	for _, volume := range pod.Spec.Volumes {
		if volume.PersistentVolumeClaim != nil {
			claimName = volume.PersistentVolumeClaim.ClaimName
		}
	}
	if claimName == "" {
		return fmt.Errorf("unable to determine PVC name for pod %s", pod.Name)
	}

	c.Log.Infof("Recreating MinIO pod %s", pod.Name)

	err := c.Config.Client.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, claimName, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete MinIO pod %s's PVC %s: %w", pod.Name, claimName, err)
	}

	err = c.Config.Client.CoreV1().Pods(ns).Delete(ctx, pod.Name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete MinIO pod %s: %w", pod.Name, err)
	}
	return nil
}

// haMinioHealthy returns true if the ha-minio statefulset is currently in a condition to accept reads and writes
// This uses https://min.io/docs/minio/linux/operations/monitoring/healthcheck-probe.html#id2
// to check if the cluster is accepting writes, specifically http://ha-minio.<namespace>.svc.cluster.local/minio/health/cluster
// 200 is healthy, 503 is not.
func (c *Controller) haMinioHealthy(ns string) bool {
	resp, err := http.Get(fmt.Sprintf("http://ha-minio.%s.svc.cluster.local/minio/health/cluster", ns))
	if err != nil {
		c.Log.Infof("Failed to hit ha-minio endpoint: %s", err.Error())
		return false
	}

	if resp.StatusCode == 200 {
		return true
	}
	return false
}

// haMinioPodSafeToReschedule returns true if the minio pod at the provided IP can be removed safely.
// This uses https://min.io/docs/minio/linux/operations/monitoring/healthcheck-probe.html#id4
// to check if the cluster would stay healthy when doing this.
// http://individual-server-address:9000/minio/health/cluster?maintenance=true (200 is ok, 412 is not)
func (c *Controller) haMinioPodSafeToReschedule(pod corev1.Pod) bool {
	resp, err := http.Get(fmt.Sprintf("http://%s:9000/minio/health/cluster?maintenance=true", pod.Status.PodIP))
	if err != nil {
		c.Log.Infof("Failed to hit ha-minio pod %s: %s", pod.Name, err.Error())
		return false
	}

	if resp.StatusCode == 200 {
		return true
	}
	c.Log.Infof("Not removing ha-minio pod %s because the cluster would not be healthy afterwards", pod.Name)
	return false
}

func (c *Controller) getMinioScale(ctx context.Context, ns string) (currentScale, desiredScale int32, reterr error) {
	currentMinioSS, err := c.Config.Client.AppsV1().StatefulSets(ns).Get(ctx, "ha-minio", metav1.GetOptions{})
	if err != nil {
		return 0, 0, fmt.Errorf("get ha-minio statefulset: %w", err)
	}

	scaleString := currentMinioSS.Annotations["kurl.sh/desired-scale"]
	scaleInt, err := strconv.ParseInt(scaleString, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to decode desired scale %q: %w", scaleString, err)
	}
	desiredScale = int32(scaleInt)

	if currentMinioSS.Spec.Replicas != nil {
		currentScale = *currentMinioSS.Spec.Replicas
	}
	return currentScale, desiredScale, nil
}
