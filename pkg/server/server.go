package server

import (
	"bufio"
	"context"
	"fmt"
	"github.com/replicatedhq/ekco/pkg/cluster/types"
	"io"
	"log"
	"net/http"
	"sync"

	"github.com/replicatedhq/ekco/pkg/cluster"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	"github.com/replicatedhq/ekco/pkg/ekcoops/overrides"
	"github.com/replicatedhq/ekco/pkg/objectstore"
	"github.com/replicatedhq/ekco/pkg/util"
	"github.com/replicatedhq/pvmigrate/pkg/migrate"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
)

const (
	MIGRATION_STATUS_OBJECTSTORE = "running object storage migration"
	MIGRATION_STATUS_PVCMIGRATE  = "running pvc migration"
	MIGRATION_STATUS_FAILED      = "failed"
	MIGRATION_STATUS_COMPLETED   = "completed"
)

var migrateStorageMut = sync.Mutex{}
var migrationStatus = ""
var migrationLogs = ""

func Serve(config ekcoops.Config, client *cluster.Controller) {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("/storagemigration/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(migrationStatus))
		if err != nil {
			log.Printf("write status: %v", err)
		}
	})

	http.HandleFunc("/storagemigration/logs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(migrationLogs))
		if err != nil {
			log.Printf("write logs: %v", err)
		}
	})

	http.HandleFunc("/storagemigration/approve", func(w http.ResponseWriter, r *http.Request) {
		go migrateStorage(config, client.Config)

		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("APPROVED"))
		if err != nil {
			log.Printf("write approval: %v", err)
		}
	})

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatalf("start server: %v", err)
	}
}

func migrateStorage(config ekcoops.Config, controllers types.ControllerConfig) {
	migrateStorageMut.Lock()
	defer migrateStorageMut.Unlock()
	if migrationStatus == MIGRATION_STATUS_COMPLETED {
		return
	}

	migrationLogs += "starting storage migration\n"

	ctx, cancel := context.WithCancel(context.Background()) // TODO maybe allow cancelling this somehow
	defer cancel()

	// TODO: pause the operator loop

	migrationStatus = MIGRATION_STATUS_OBJECTSTORE
	err := migrateObjectStorage(ctx, config, controllers)
	if err != nil {
		migrationStatus = MIGRATION_STATUS_FAILED
		migrationLogs += fmt.Sprintf("migrate object storage: %v", err)
		return
	}

	migrationLogs += "starting pvmigrate\n"

	migrationStatus = MIGRATION_STATUS_PVCMIGRATE
	err = migrateStorageClasses(ctx, config, controllers)
	if err != nil {
		migrationStatus = MIGRATION_STATUS_FAILED
		migrationLogs += fmt.Sprintf("migrate storage classes: %v", err)
		return
	}

	migrationLogs += "storage migration completed\n"

	migrationStatus = MIGRATION_STATUS_COMPLETED
}

// check if minio is in use
// if so, disable the minio service to prevent race conditions with files being updated during the migration
// then, migrate all data from minio to rook
// then, update secrets in the cluster to point to the new rook object store
// finally, delete the minio namespace and contents
func migrateObjectStorage(ctx context.Context, config ekcoops.Config, controllers types.ControllerConfig) error {
	client := controllers.Client
	minioInUse, err := objectstore.IsMinioInUse(context.TODO(), client, config.MinioNamespace)
	if err != nil {
		return fmt.Errorf("check if minio is in use: %v", err)
	}
	if !minioInUse {
		return nil
	}

	// discover the IP address of the existing minio pod to migrate from
	minioPodIP := ""
	minioPods, err := client.CoreV1().Pods(config.MinioNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=minio"})
	if err != nil {
		return fmt.Errorf("list minio pods: %w", err)
	}
	if len(minioPods.Items) == 0 {
		return fmt.Errorf("unable to find existing minio pod to migrate from")
	}
	minioPodIP = minioPods.Items[0].Status.PodIP

	// get the minio credentials to be used for the migration
	credentialSecret, err := client.CoreV1().Secrets(config.MinioNamespace).Get(ctx, "minio-credentials", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("retrieve minio credentials: %w", err)
	}

	minioAccessKey := string(credentialSecret.Data["MINIO_ACCESS_KEY"])
	minioSecretKey := string(credentialSecret.Data["MINIO_SECRET_KEY"])

	rookService, err := client.CoreV1().Services("rook-ceph").Get(ctx, "rook-ceph-rgw-rook-ceph-store", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("retrieve rook object store endpoint: %w", err)
	}
	rookEndpoint := rookService.Spec.ClusterIP

	rookSecret, err := client.CoreV1().Secrets("rook-ceph").Get(ctx, "rook-ceph-object-user-rook-ceph-store-kurl", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("retrieve rook object store credentials: %w", err)
	}
	rookAccessKey := string(rookSecret.Data["AccessKey"])
	rookSecretKey := string(rookSecret.Data["SecretKey"])

	// disable minio service
	doesNotExistSelector := `
[ { "op": "replace", "path": "/spec/selector", "value": {"doesnotexist": "doesnotexist"} } ]
`
	_, err = client.CoreV1().Services(config.MinioNamespace).Patch(ctx, "minio", apitypes.JSONPatchType, []byte(doesNotExistSelector), metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("disable existing minio service: %w", err)
	}

	logsChan := make(chan string)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case logLine := <-logsChan:
				migrationLogs += logLine + "\n"
			}
		}
	}()

	// migrate all data from minio to rook
	err = objectstore.SyncAllBuckets(ctx, fmt.Sprintf("%s:9000", minioPodIP), minioAccessKey, minioSecretKey, rookEndpoint, rookAccessKey, rookSecretKey, logsChan)
	if err != nil {
		return fmt.Errorf("migrate data from minio to rook: %w", err)
	}

	// update secrets in the cluster to point to the new rook object store
	migrationLogs += "updating secrets in the cluster to point to the new rook object store\n"
	err = objectstore.UpdateConsumers(ctx, controllers, rookEndpoint, "http://rook-ceph-rgw-rook-ceph-store.rook-ceph", rookAccessKey, rookSecretKey, fmt.Sprintf("minio.%s", config.MinioNamespace), minioSecretKey)
	if err != nil {
		return fmt.Errorf("update secrets in the cluster to point to the new rook object store: %w", err)
	}

	// delete the minio namespace and contents
	migrationLogs += "deleting the minio namespace and contents\n"
	err = client.AppsV1().StatefulSets(config.MinioNamespace).Delete(ctx, "ha-minio", metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("delete minio statefulset: %v", err)
	}

	err = client.AppsV1().Deployments(config.MinioNamespace).Delete(ctx, "minio", metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("delete minio deployment: %v", err)
	}

	err = client.CoreV1().Namespaces().Delete(ctx, config.MinioNamespace, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("delete minio namespace: %v", err)
	}

	return nil
}

func migrateStorageClasses(ctx context.Context, config ekcoops.Config, controllers types.ControllerConfig) error {
	client := controllers.Client

	logsReader, logsWriter := io.Pipe()
	go func() {
		bufReader := bufio.NewReader(logsReader)
		for line, err := bufReader.ReadString('\n'); err == nil; {
			migrationLogs += string(line) + "\n"
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

	err := util.ScalePrometheus(controllers.PrometheusV1, 0)
	if err != nil {
		return fmt.Errorf("scale down prometheus: %v", err)
	}

	err = migrate.Migrate(ctx, fileLog, client, options)
	if err != nil {
		return fmt.Errorf("run pvmigrate: %v", err)
	}

	err = client.StorageV1().StorageClasses().Delete(ctx, "scaling", metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("delete scaling storage class: %v", err)
	}

	err = util.ScalePrometheus(controllers.PrometheusV1, 2)
	if err != nil {
		return fmt.Errorf("scale up prometheus: %v", err)
	}

	return nil
}
