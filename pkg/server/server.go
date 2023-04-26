package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	"github.com/replicatedhq/ekco/pkg/util"
	"github.com/replicatedhq/pvmigrate/pkg/migrate"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
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

func Serve(config ekcoops.Config, client kubernetes.Interface) {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("/storagemigration/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(migrationStatus))
		if err != nil {
			log.Printf("failed to write status: %v", err)
		}
	})

	http.HandleFunc("/storagemigration/logs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(migrationLogs))
		if err != nil {
			log.Printf("failed to write logs: %v", err)
		}
	})

	http.HandleFunc("/storagemigration/approve", func(w http.ResponseWriter, r *http.Request) {
		go migrateStorage(config, client)

		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte("APPROVED"))
		if err != nil {
			log.Printf("failed to write approval: %v", err)
		}
	})

	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}

func migrateStorage(config ekcoops.Config, client kubernetes.Interface) {
	migrateStorageMut.Lock()
	defer migrateStorageMut.Unlock()
	if migrationStatus == MIGRATION_STATUS_COMPLETED {
		return
	}

	fmt.Printf("starting storage migration\n")

	// TODO: pause the operator loop

	migrationStatus = MIGRATION_STATUS_OBJECTSTORE
	err := migrateObjectStorage(config, client)
	if err != nil {
		migrationStatus = MIGRATION_STATUS_FAILED
		migrationLogs = fmt.Sprintf("failed to migrate object storage: %v", err)
		return
	}

	fmt.Printf("starting pvmigrate\n")

	migrationStatus = MIGRATION_STATUS_PVCMIGRATE
	err = migrateStorageClasses(config, client)
	if err != nil {
		migrationStatus = MIGRATION_STATUS_FAILED
		migrationLogs = fmt.Sprintf("failed to migrate storage classes: %v", err)
		return
	}

	fmt.Printf("storage migration completed\n")

	migrationStatus = MIGRATION_STATUS_COMPLETED
}

// TODO move to 'minio' package, call from 'cluster' too
func isMinioInUse(config ekcoops.Config, client kubernetes.Interface) (bool, error) {
	// if the minio NS does not exist, it is not in use
	_, err := client.CoreV1().Namespaces().Get(context.TODO(), config.MinioNamespace, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		} else {
			return false, fmt.Errorf("failed to get minio namespace %s: %v", config.MinioNamespace, err)
		}
	}

	// if the minio deployment and statefulset do not exist, there is nothing to migrate
	_, err = client.AppsV1().Deployments(config.MinioNamespace).Get(context.TODO(), "minio", metav1.GetOptions{})
	if err == nil {
		return true, nil
	} else {
		if !k8serrors.IsNotFound(err) {
			return false, fmt.Errorf("failed to get minio deployment in %s: %v", config.MinioNamespace, err)
		}
	}

	_, err = client.AppsV1().StatefulSets(config.MinioNamespace).Get(context.TODO(), "ha-minio", metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		} else {
			return false, fmt.Errorf("failed to get minio statefulset in %s: %v", config.MinioNamespace, err)
		}
	}

	return true, nil
}

// check if minio is in use
// if so, disable the minio service to prevent race conditions with files being updated during the migration
// then, migrate all data from minio to rook
// then, update secrets in the cluster to point to the new rook object store
// finally, delete the minio namespace and contents
// TODO factor out, call from cluster package too
func migrateObjectStorage(config ekcoops.Config, client kubernetes.Interface) error {
	minioInUse, err := isMinioInUse(config, client)
	if err != nil {
		return fmt.Errorf("failed to check if minio is in use: %v", err)
	}
	if !minioInUse {
		return nil
	}

	// discover the IP address of the existing minio pod to migrate from
	minioPodIP := ""
	minioPods, err := client.CoreV1().Pods(config.MinioNamespace).List(context.TODO(), metav1.ListOptions{LabelSelector: "app=minio"})
	if err != nil {
		return fmt.Errorf("list minio pods: %w", err)
	}
	if len(minioPods.Items) == 0 {
		return fmt.Errorf("unable to find existing minio pod to migrate from")
	}
	minioPodIP = minioPods.Items[0].Status.PodIP

	// get the minio credentials to be used for the migration
	credentialSecret, err := client.CoreV1().Secrets(config.MinioNamespace).Get(context.TODO(), "minio-credentials", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("retrieve minio credentials: %w", err)
	}

	minioAccessKey := string(credentialSecret.Data["MINIO_ACCESS_KEY"])
	minioSecretKey := string(credentialSecret.Data["MINIO_SECRET_KEY"])

	rookService, err := client.CoreV1().Services("rook-ceph").Get(context.TODO(), "rook-ceph-rgw-rook-ceph-store", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("retrieve rook object store endpoint: %w", err)
	}
	rookEndpoint := rookService.Spec.ClusterIP

	rookSecret, err := client.CoreV1().Secrets("rook-ceph").Get(context.TODO(), "rook-ceph-object-user-rook-ceph-store-kurl", metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("retrieve rook object store credentials: %w", err)
	}
	rookAccessKey := string(rookSecret.Data["AccessKey"])
	rookSecretKey := string(rookSecret.Data["SecretKey"])

	// disable minio service
	doesNotExistSelector := `
[ { "op": "replace", "path": "/spec/selector", "value": {"doesnotexist": "doesnotexist"} } ]
`
	_, err = client.CoreV1().Services(config.MinioNamespace).Patch(context.TODO(), "minio", apitypes.JSONPatchType, []byte(doesNotExistSelector), metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("disable existing minio service: %w", err)
	}

	// Initialize minio client object.
	minioClient, err := minio.New(fmt.Sprintf("%s:9000", minioPodIP), &minio.Options{
		Creds: credentials.NewStaticV4(minioAccessKey, minioSecretKey, ""),
	})
	if err != nil {
		return fmt.Errorf("failed to initialize minio client: %v", err)
	}

	// Initialize rook client object.
	rookClient, err := minio.New(rookEndpoint, &minio.Options{
		Creds: credentials.NewStaticV4(rookAccessKey, rookSecretKey, ""),
	})
	if err != nil {
		return fmt.Errorf("failed to initialize rook client: %v", err)
	}

	fmt.Printf("determining buckets to migrate\n")

	minioBuckets, err := minioClient.ListBuckets(context.TODO())
	if err != nil {
		return fmt.Errorf("failed to list minio buckets: %v", err)
	}

	for _, bucket := range minioBuckets {
		fmt.Printf("migrating bucket %s\n", bucket.Name)
		numObjects, err := syncBucket(context.TODO(), minioClient, rookClient, bucket.Name)
		if err != nil {
			return fmt.Errorf("failed to sync bucket %s: %v", bucket.Name, err)
		}
		migrationLogs += fmt.Sprintf("synced %s objects in bucket %s\n", numObjects, bucket.Name)
	}

	// update secrets in the cluster to point to the new rook object store
	fmt.Printf("updating secrets in the cluster to point to the new rook object store\n")

	// if kubernetes_resource_exists default secret kotsadm-s3
	kotsadmS3, err := client.CoreV1().Secrets("default").Get(context.TODO(), "kotsadm-s3", metav1.GetOptions{})
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to get kotsadm-s3 secret in default namespace: %v", err)
		}
	}
	if kotsadmS3 != nil {
		kotsadmS3.Data["access-key-id"] = []byte(rookAccessKey)
		kotsadmS3.Data["secret-access-key"] = []byte(rookSecretKey)
		kotsadmS3.Data["endpoint"] = []byte("http://rook-ceph-rgw-rook-ceph-store.rook-ceph")
		kotsadmS3.Data["object-store-cluster-ip"] = []byte(rookEndpoint)

		_, err = client.CoreV1().Secrets("default").Update(context.TODO(), kotsadmS3, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update kotsadm-s3 secret in default namespace: %v", err)
		}

		err = util.RestartDeployment(context.TODO(), client, "default", "kotsadm")
		if err != nil {
			return fmt.Errorf("failed to restart kotsadm deployment after migrating object store: %v", err)
		}
		err = util.RestartStatefulset(context.TODO(), client, "default", "kotsadm")
		if err != nil {
			return fmt.Errorf("failed to restart kotsadm statefulset after migrating object store: %v", err)
		}
	}

	// if the 'registry-config' configmap and the 'registry-s3-secret' secret exists in the kurl namespace
	registryConfig, err := client.CoreV1().ConfigMaps("kurl").Get(context.TODO(), "registry-config", metav1.GetOptions{})
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to get registry-config configmap in kurl namespace: %v", err)
		}
	}
	registrySecret, err := client.CoreV1().Secrets("kurl").Get(context.TODO(), "registry-s3-secret", metav1.GetOptions{})
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to get registry-s3-secret secret in kurl namespace: %v", err)
		}
	}
	if registryConfig != nil && registrySecret != nil {
		existingConfig := registryConfig.Data["config.yml"]
		newConfig := regexp.MustCompile(`regionendpoint: http.*`).ReplaceAllString(existingConfig, fmt.Sprintf("regionendpoint: http://%s/", "TODO ROOK OBJECT STORE CLUSTER IP"))
		registryConfig.Data["config.yml"] = newConfig

		_, err = client.CoreV1().ConfigMaps("kurl").Update(context.TODO(), registryConfig, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update registry-config configmap in kurl namespace: %v", err)
		}

		registrySecret.StringData["access-key-id"] = rookAccessKey
		registrySecret.StringData["secret-access-key"] = rookSecretKey
		registrySecret.StringData["object-store-cluster-ip"] = rookEndpoint
		registrySecret.StringData["object-store-hostname"] = "http://rook-ceph-rgw-rook-ceph-store.rook-ceph"

		_, err = client.CoreV1().Secrets("kurl").Update(context.TODO(), registrySecret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update registry-s3-secret secret in kurl namespace: %v", err)
		}

		err = util.RestartDeployment(context.TODO(), client, "registry", "kurl")
		if err != nil {
			return fmt.Errorf("failed to restart registry deployment after migrating object store: %v", err)
		}
	}

	// if kubernetes_resource_exists velero backupstoragelocation default TODO

	// if kubernetes_resource_exists velero secret cloud-credentials TODO

	err = client.AppsV1().StatefulSets(config.MinioNamespace).Delete(context.TODO(), "ha-minio", metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete minio statefulset: %v", err)
	}

	err = client.AppsV1().Deployments(config.MinioNamespace).Delete(context.TODO(), "minio", metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete minio deployment: %v", err)
	}

	err = client.CoreV1().Namespaces().Delete(context.TODO(), config.MinioNamespace, metav1.DeleteOptions{})
	if err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete minio namespace: %v", err)
	}

	return nil
}

func syncBucket(ctx context.Context, src *minio.Client, dst *minio.Client, bucket string) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	count := 0

	exists, err := dst.BucketExists(ctx, bucket)
	if err != nil {
		return count, fmt.Errorf("Failed to check if bucket %q exists in destination: %v", bucket, err)
	}
	if !exists {
		if err := dst.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return count, fmt.Errorf("Failed to make bucket %q in destination: %v", bucket, err)
		}
	}

	srcObjectInfoChan := src.ListObjects(ctx, bucket, minio.ListObjectsOptions{})

	for srcObjectInfo := range srcObjectInfoChan {
		srcObject, err := src.GetObject(ctx, bucket, srcObjectInfo.Key, minio.GetObjectOptions{})
		if err != nil {
			return count, fmt.Errorf("Get %s from source: %v", srcObjectInfo.Key, err)
		}

		_, err = dst.PutObject(ctx, bucket, srcObjectInfo.Key, srcObject, srcObjectInfo.Size, minio.PutObjectOptions{
			ContentType:     srcObjectInfo.ContentType,
			ContentEncoding: srcObjectInfo.Metadata.Get("Content-Encoding"),
		})
		srcObject.Close()
		if err != nil {
			return count, fmt.Errorf("Failed to copy object %s to destination: %v", srcObjectInfo.Key, err)
		}

		count++
	}

	return count, nil
}

func migrateStorageClasses(config ekcoops.Config, client kubernetes.Interface) error {
	fileLog := log.New(nil, "", 0) // TODO: save to logs string

	options := migrate.Options{
		SourceSCName: "scaling",
		DestSCName:   config.RookStorageClass,
		RsyncImage:   config.MinioUtilImage,
		SetDefaults:  true,
	}

	migrationStatus = MIGRATION_STATUS_PVCMIGRATE

	err := migrate.Migrate(context.TODO(), fileLog, client, options)
	if err != nil {
		return fmt.Errorf("failed to run pvmigrate: %v", err)
	}

	err = client.StorageV1().StorageClasses().Delete(context.TODO(), "scaling", metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete scaling storage class: %v", err)
	}
	return nil
}
