package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	"github.com/replicatedhq/pvmigrate/pkg/migrate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	migrationStatus = MIGRATION_STATUS_OBJECTSTORE
	err := migrateObjectStorage(config, client)
	if err != nil {
		migrationStatus = MIGRATION_STATUS_FAILED
		migrationLogs = fmt.Sprintf("failed to migrate object storage: %v", err)
		return
	}

	migrationStatus = MIGRATION_STATUS_PVCMIGRATE
	err = migrateStorageClasses(config, client)
	if err != nil {
		migrationStatus = MIGRATION_STATUS_FAILED
		migrationLogs = fmt.Sprintf("failed to migrate storage classes: %v", err)
		return
	}

	migrationStatus = MIGRATION_STATUS_COMPLETED
}

// check if minio is in use
// if so, disable the minio service to prevent race conditions with files being updated during the migration
// then, migrate all data from minio to rook
// then, delete the minio namespace and contents
func migrateObjectStorage(config ekcoops.Config, client kubernetes.Interface) error {
	// TODO: check if minio is in use

	// TODO: replace with actual, live minio creds
	endpoint := "play.min.io"
	accessKeyID := "Q3AM3UQ867SPQQA43P2F"
	secretAccessKey := "zuf+tfteSlswRu7BJ86wekitnifILbZam1KYY3TG"
	// TODO: rook creds

	// TODO: disable minio service

	// Initialize minio client object.
	minioClient, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
	})
	if err != nil {
		return fmt.Errorf("failed to initialize minio client: %v", err)
	}

	rookClient, err := minio.New(endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
	})
	if err != nil {
		return fmt.Errorf("failed to initialize rook client: %v", err)
	}

	minioBuckets, err := minioClient.ListBuckets(context.TODO())
	if err != nil {
		return fmt.Errorf("failed to list minio buckets: %v", err)
	}

	for _, bucket := range minioBuckets {
		numObjects, err := syncBucket(context.TODO(), minioClient, rookClient, bucket.Name)
		if err != nil {
			return fmt.Errorf("failed to sync bucket %s: %v", bucket.Name, err)
		}
		migrationLogs += fmt.Sprintf("synced %s objects in bucket %s\n", numObjects, bucket.Name)
	}

	// TODO: delete minio

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
		if err := dst.MakeBucket(ctx, bucket, ""); err != nil {
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
