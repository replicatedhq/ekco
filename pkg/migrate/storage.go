package migrate

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/replicatedhq/ekco/pkg/cluster"
	"github.com/replicatedhq/ekco/pkg/cluster/types"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	"github.com/replicatedhq/ekco/pkg/ekcoops/overrides"
	"github.com/replicatedhq/ekco/pkg/objectstore"
	"github.com/replicatedhq/ekco/pkg/util"
	"github.com/replicatedhq/pvmigrate/pkg/migrate"
	cephv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"github.com/rook/rook/pkg/daemon/ceph/client"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	types2 "k8s.io/apimachinery/pkg/types"
)

const (
	MIGRATION_STATUS_OBJECTSTORE = "running object storage migration"
	MIGRATION_STATUS_PVCMIGRATE  = "running pvc migration"
	MIGRATION_STATUS_FAILED      = "failed"
	MIGRATION_STATUS_COMPLETED   = "completed"
	MIGRATION_STATUS_NOT_STARTED = "not started"
	MIGRATION_STATUS_NOT_READY   = "not ready"
)

var migrateStorageMut = sync.Mutex{}
var migrationStatus = MIGRATION_STATUS_NOT_STARTED
var migrationLogs = ""

// ObjectStorageAndPVCs migrates the object storage from MinIO to Rook, and migrates PVCs from 'scaling' to the Rook storageclass
func ObjectStorageAndPVCs(config ekcoops.Config, controllers types.ControllerConfig) {
	migrateStorageMut.Lock()
	defer migrateStorageMut.Unlock()
	if migrationStatus == MIGRATION_STATUS_COMPLETED {
		return
	}
	ctx, cancel := context.WithCancel(context.Background()) // TODO maybe allow cancelling this somehow
	defer cancel()

	setLogs("checking if the cluster is ready to migrate\n")
	status, err := IsMigrationReady(ctx, config, controllers)
	if err != nil {
		migrationStatus = MIGRATION_STATUS_FAILED
		addLogs("is migration ready: %v", err)
		return
	}
	if !status.Ready {
		migrationStatus = MIGRATION_STATUS_NOT_READY
		addLogs("not ready: %s", status.Reason)
		return
	}

	addLogs("starting object storage migration")

	migrationStatus = MIGRATION_STATUS_OBJECTSTORE
	err = migrateObjectStorage(ctx, config.MinioNamespace, controllers)
	if err != nil {
		migrationStatus = MIGRATION_STATUS_FAILED
		addLogs("failed to migrate object storage: %v", err)
		return
	}

	addLogs("starting persistent volume migration")

	migrationStatus = MIGRATION_STATUS_PVCMIGRATE
	err = migrateStorageClasses(ctx, config, controllers)
	if err != nil {
		migrationStatus = MIGRATION_STATUS_FAILED
		addLogs("failed to migrate storage classes: %v", err)
		return
	}

	addLogs("storage migration completed")

	migrationStatus = MIGRATION_STATUS_COMPLETED
}

func GetMigrationStatus() string {
	return migrationStatus
}

func GetMigrationLogs() string {
	return migrationLogs
}

// MigrationReadyStatus represents the status of the migration readiness check, includes
// a reason, the total number of nodes in the cluster and the required number of nodes needed to start the migration.
type MigrationReadyResult struct {
	Ready           bool   `json:"ready"`
	Reason          string `json:"reason"`
	NrNodes         int    `json:"nrNodes"`
	RequiredNrNodes int    `json:"requiredNrNodes"`
}

// IsMigrationReady returns if the cluster is ready to migrate storage. This is true if Ceph is setup/healthy, the ceph storageclass is
// present in the cluster, and the ceph object store user exists. This function also returns the current of number of nodes in the cluster.
func IsMigrationReady(ctx context.Context, config ekcoops.Config, controllers types.ControllerConfig) (*MigrationReadyResult, error) {
	nodes, err := controllers.Client.CoreV1().Nodes().List(ctx, v1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("get nodes: %w", err)
	}
	nrnodes := len(nodes.Items)

	// get the cephcluster - if it doesn't exist, we can't migrate
	cephCluster, err := controllers.CephV1.CephClusters(cluster.RookCephNS).Get(ctx, cluster.CephClusterName, v1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return &MigrationReadyResult{RequiredNrNodes: config.RookMinimumNodeCount, NrNodes: nrnodes, Reason: "ceph cluster not found"}, nil
		}
		return nil, fmt.Errorf("get ceph cluster: %w", err)
	}

	if cephCluster.Status.Phase != cephv1.ConditionReady {
		return &MigrationReadyResult{RequiredNrNodes: config.RookMinimumNodeCount, NrNodes: nrnodes, Reason: fmt.Sprintf("ceph cluster was %s, not ready", cephCluster.Status.Phase)}, nil
	} else if cephCluster.Status.CephStatus != nil && cephCluster.Status.CephStatus.Health != client.CephHealthOK {
		return &MigrationReadyResult{RequiredNrNodes: config.RookMinimumNodeCount, NrNodes: nrnodes, Reason: fmt.Sprintf("ceph cluster was %s, not healthy", cephCluster.Status.CephStatus.Health)}, nil
	}

	// get the ceph object store secret - if it doesn't exist, we can't migrate
	_, err = controllers.Client.CoreV1().Secrets(cluster.RookCephNS).Get(ctx, "rook-ceph-object-user-rook-ceph-store-kurl", v1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return &MigrationReadyResult{RequiredNrNodes: config.RookMinimumNodeCount, NrNodes: nrnodes, Reason: "ceph object store secret not found"}, nil
		}
		return nil, fmt.Errorf("get ceph object store secret: %w", err)
	}

	// get the ceph storageclass - if it doesn't exist, we can't migrate
	_, err = controllers.Client.StorageV1().StorageClasses().Get(ctx, config.RookStorageClass, v1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return &MigrationReadyResult{RequiredNrNodes: config.RookMinimumNodeCount, NrNodes: nrnodes, Reason: fmt.Sprintf("rook storageclass %s not found", config.RookStorageClass)}, nil
		}
		return nil, fmt.Errorf("get ceph storageclass: %w", err)
	}

	return &MigrationReadyResult{Ready: true, Reason: "migration ready", NrNodes: nrnodes, RequiredNrNodes: config.RookMinimumNodeCount}, nil
}

// check if minio is in use
// if so, disable the minio service to prevent race conditions with files being updated during the migration
// then, migrate all data from minio to rook
// then, update secrets in the cluster to point to the new rook object store
// finally, delete the minio namespace and contents
func migrateObjectStorage(ctx context.Context, minioNS string, controllers types.ControllerConfig) error {
	client := controllers.Client
	minioInUse, err := objectstore.IsMinioInUse(ctx, client, minioNS)
	if err != nil {
		return fmt.Errorf("check if minio is in use: %v", err)
	}
	if !minioInUse {
		return nil
	}

	overrides.PauseMinIO()
	defer overrides.ResumeMinIO()

	// discover the IP address of the existing minio pod to migrate from
	minioPodIP := ""
	minioPods, err := client.CoreV1().Pods(minioNS).List(ctx, v1.ListOptions{LabelSelector: "app=minio"})
	if err != nil {
		return fmt.Errorf("list minio pods: %w", err)
	}
	if len(minioPods.Items) == 0 {
		// while we may not have found minio running as a single pod, it may be running as a statefulset - which has a different label
		minioPods, err = client.CoreV1().Pods(minioNS).List(ctx, v1.ListOptions{LabelSelector: "app=ha-minio"})
		if err != nil {
			return fmt.Errorf("list ha-minio pods: %w", err)
		}

		if len(minioPods.Items) == 0 {
			return fmt.Errorf("unable to find existing minio pod to migrate from")
		}
	}
	for _, pod := range minioPods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			minioPodIP = pod.Status.PodIP
			break
		}
	}
	if minioPodIP == "" {
		return fmt.Errorf("unable to find running minio pod to migrate from")
	}

	// get the minio credentials to be used for the migration
	credentialSecret, err := client.CoreV1().Secrets(minioNS).Get(ctx, "minio-credentials", v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("retrieve minio credentials: %w", err)
	}

	minioAccessKey := string(credentialSecret.Data["MINIO_ACCESS_KEY"])
	minioSecretKey := string(credentialSecret.Data["MINIO_SECRET_KEY"])

	rookService, err := client.CoreV1().Services(cluster.RookCephNS).Get(ctx, "rook-ceph-rgw-rook-ceph-store", v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("retrieve rook object store endpoint: %w", err)
	}
	rookEndpoint := rookService.Spec.ClusterIP

	rookSecret, err := client.CoreV1().Secrets(cluster.RookCephNS).Get(ctx, "rook-ceph-object-user-rook-ceph-store-kurl", v1.GetOptions{})
	if err != nil {
		return fmt.Errorf("retrieve rook object store credentials: %w", err)
	}
	rookAccessKey := string(rookSecret.Data["AccessKey"])
	rookSecretKey := string(rookSecret.Data["SecretKey"])

	// disable minio service
	doesNotExistSelector := `
[ { "op": "replace", "path": "/spec/selector", "value": {"doesnotexist": "doesnotexist"} } ]
`
	_, err = client.CoreV1().Services(minioNS).Patch(ctx, "minio", types2.JSONPatchType, []byte(doesNotExistSelector), v1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("disable existing minio service: %w", err)
	}

	// migrate all data from minio to rook
	err = objectstore.SyncAllBuckets(ctx, fmt.Sprintf("%s:9000", minioPodIP), minioAccessKey, minioSecretKey, rookEndpoint, rookAccessKey, rookSecretKey, addLogs)
	if err != nil {
		return fmt.Errorf("migrate data from minio to rook: %w", err)
	}

	// update secrets in the cluster to point to the new rook object store
	addLogs("updating secrets in the cluster to point to the new rook object store")
	err = objectstore.UpdateConsumers(ctx, controllers, addLogs, rookEndpoint, "http://rook-ceph-rgw-rook-ceph-store.rook-ceph", rookAccessKey, rookSecretKey, fmt.Sprintf("minio.%s", minioNS), minioSecretKey)
	if err != nil {
		return fmt.Errorf("update secrets in the cluster to point to the new rook object store: %w", err)
	}

	// delete the minio namespace and contents
	addLogs("deleting the minio namespace and contents")
	err = client.AppsV1().StatefulSets(minioNS).Delete(ctx, "ha-minio", v1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete minio statefulset: %v", err)
	}

	err = client.AppsV1().Deployments(minioNS).Delete(ctx, "minio", v1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete minio deployment: %v", err)
	}

	err = client.CoreV1().Namespaces().Delete(ctx, minioNS, v1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete minio namespace: %v", err)
	}

	return nil
}

func migrateStorageClasses(ctx context.Context, config ekcoops.Config, controllers types.ControllerConfig) error {
	client := controllers.Client

	logsReader, logsWriter := io.Pipe()
	defer logsWriter.Close()
	go func() {
		defer logsReader.Close()
		bufScanner := bufio.NewScanner(logsReader)
		for bufScanner.Scan() {
			addLogs(bufScanner.Text())
		}
	}()
	fileLog := log.New(logsWriter, "", 0)

	options := migrate.Options{
		SourceSCName: "scaling",
		DestSCName:   config.RookStorageClass,
		RsyncImage:   config.MinioUtilImage,
		SetDefaults:  true,
	}

	migrationStatus = MIGRATION_STATUS_PVCMIGRATE

	overrides.PausePrometheus()
	defer overrides.ResumePrometheus()
	overrides.PauseKotsadm()
	defer overrides.ResumeKotsadm()

	addLogs("scaling down prometheus")
	err := util.ScalePrometheus(controllers.PrometheusV1, 0)
	if err != nil {
		return fmt.Errorf("scale down prometheus: %v", err)
	}

	addLogs("migrating data from %q storageclass to %q", "scaling", config.RookStorageClass)
	err = migrate.Migrate(ctx, fileLog, client, options)
	if err != nil {
		return fmt.Errorf("run pvmigrate: %v", err)
	}

	addLogs("deleting the (empty) %q storageclass", "scaling")
	err = client.StorageV1().StorageClasses().Delete(ctx, "scaling", v1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("delete scaling storage class: %v", err)
	}

	addLogs("scaling up prometheus")
	err = util.ScalePrometheus(controllers.PrometheusV1, 2)
	if err != nil {
		return fmt.Errorf("scale up prometheus: %v", err)
	}

	return nil
}

func addLogs(format string, args ...interface{}) {
	migrationLogs += fmt.Sprintf(format, args...) + "\n"
}

func setLogs(logs string) {
	migrationLogs = logs
}
