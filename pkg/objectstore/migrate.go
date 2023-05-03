package objectstore

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/replicatedhq/ekco/pkg/cluster/types"
	"github.com/replicatedhq/ekco/pkg/util"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// SyncAllBuckets syncs copies all objects in all buckets in object store to another, and returns progress via a channel.
func SyncAllBuckets(ctx context.Context, sourceEndpoint, sourceAccessKey, sourceSecretKey, destEndpoint, destAccessKey, destSecretKey string, logs chan<- string) error {
	// Initialize source client object.
	minioClient, err := minio.New(sourceEndpoint, &minio.Options{
		Creds: credentials.NewStaticV4(sourceAccessKey, sourceSecretKey, ""),
	})
	if err != nil {
		return fmt.Errorf("failed to initialize minio client: %v", err)
	}

	// Initialize dest client object.
	rookClient, err := minio.New(destEndpoint, &minio.Options{
		Creds: credentials.NewStaticV4(destAccessKey, destSecretKey, ""),
	})
	if err != nil {
		return fmt.Errorf("failed to initialize rook client: %v", err)
	}

	if logs != nil {
		logs <- "Initialized clients"
	}

	minioBuckets, err := minioClient.ListBuckets(ctx)
	if err != nil {
		return fmt.Errorf("failed to list minio buckets: %v", err)
	}

	if logs != nil {
		logs <- fmt.Sprintf("Found %d buckets to sync", len(minioBuckets))
	}

	for _, bucket := range minioBuckets {
		if logs != nil {
			logs <- fmt.Sprintf("Syncing objects in %s", bucket.Name)
		}

		startTime := time.Now()
		numObjects, err := syncBucket(ctx, minioClient, rookClient, bucket.Name, logs)
		if err != nil {
			return fmt.Errorf("failed to sync bucket %s: %v", bucket.Name, err)
		}

		if logs != nil {
			logs <- fmt.Sprintf("Copied %d objects in %s over %s", numObjects, bucket.Name, time.Since(startTime).String())
		}
	}

	return nil
}

// UpdateConsumers updates the access key and secret key for all consumers of the object store.
// it handles kotsadm, registry, and velero.
func UpdateConsumers(ctx context.Context, controllers types.ControllerConfig, endpoint, hostname, accessKey, secretKey, originalHostname, originalSecretKey string, logs chan<- string) error {
	client := controllers.Client
	// if kubernetes_resource_exists default secret kotsadm-s3
	err := updateKotsadmObjectStore(ctx, client, logs, accessKey, secretKey, hostname, endpoint)
	if err != nil {
		return err
	}

	err = updateRegistryObjectStore(ctx, client, logs, accessKey, secretKey, hostname, endpoint)
	if err != nil {
		return err
	}

	err = updateVeleroObjectStore(ctx, controllers, logs, accessKey, secretKey, hostname, endpoint, originalSecretKey, originalHostname)
	if err != nil {
		return err
	}

	return nil
}

func updateKotsadmObjectStore(ctx context.Context, client kubernetes.Interface, logs chan<- string, accessKey string, secretKey string, hostname string, endpoint string) error {
	kotsadmS3, err := client.CoreV1().Secrets("default").Get(ctx, "kotsadm-s3", metav1.GetOptions{})
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("get kotsadm-s3 secret in default namespace: %v", err)
		}
	}
	if kotsadmS3 != nil {
		kotsadmS3.Data["access-key-id"] = []byte(accessKey)
		kotsadmS3.Data["secret-access-key"] = []byte(secretKey)
		kotsadmS3.Data["endpoint"] = []byte(hostname)
		kotsadmS3.Data["object-store-cluster-ip"] = []byte(endpoint)

		if logs != nil {
			logs <- "Updating kotsadm-s3 secret to use new object store"
		}

		_, err = client.CoreV1().Secrets("default").Update(ctx, kotsadmS3, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("update kotsadm-s3 secret in default namespace: %v", err)
		}

		if logs != nil {
			logs <- "Restarting kotsadm"
		}

		err = util.RestartDeployment(ctx, client, "default", "kotsadm")
		if err != nil {
			return fmt.Errorf("restart kotsadm deployment after migrating object store: %v", err)
		}
		err = util.RestartStatefulSet(ctx, client, "default", "kotsadm")
		if err != nil {
			return fmt.Errorf("restart kotsadm statefulset after migrating object store: %v", err)
		}

		if logs != nil {
			logs <- "Kotsadm restarted"
		}
	}
	return nil
}

func updateRegistryObjectStore(ctx context.Context, client kubernetes.Interface, logs chan<- string, accessKey string, secretKey string, hostname string, endpoint string) error {
	// if the 'registry-config' configmap and the 'registry-s3-secret' secret exists in the kurl namespace
	registryConfig, err := client.CoreV1().ConfigMaps("kurl").Get(ctx, "registry-config", metav1.GetOptions{})
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("get registry-config configmap in kurl namespace: %v", err)
		}
	}
	registrySecret, err := client.CoreV1().Secrets("kurl").Get(ctx, "registry-s3-secret", metav1.GetOptions{})
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("get registry-s3-secret secret in kurl namespace: %v", err)
		}
	}
	if registryConfig != nil && registrySecret != nil {
		if logs != nil {
			logs <- "Updating Registry to use new object store"
		}

		existingConfig := registryConfig.Data["config.yml"]
		newConfig := regexp.MustCompile(`regionendpoint: http.*`).ReplaceAllString(existingConfig, fmt.Sprintf("regionendpoint: http://%s/", endpoint))
		registryConfig.Data["config.yml"] = newConfig

		_, err = client.CoreV1().ConfigMaps("kurl").Update(ctx, registryConfig, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("update registry-config configmap in kurl namespace: %v", err)
		}

		registrySecret.Data["access-key-id"] = []byte(accessKey)
		registrySecret.Data["secret-access-key"] = []byte(secretKey)
		registrySecret.Data["object-store-cluster-ip"] = []byte(endpoint)
		registrySecret.Data["object-store-hostname"] = []byte(hostname)

		_, err = client.CoreV1().Secrets("kurl").Update(ctx, registrySecret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("update registry-s3-secret secret in kurl namespace: %v", err)
		}

		if logs != nil {
			logs <- "Restarting Registry"
		}

		err = util.RestartDeployment(ctx, client, "kurl", "registry")
		if err != nil {
			return fmt.Errorf("restart registry deployment after migrating object store: %v", err)
		}

		if logs != nil {
			logs <- "Registry restarted"
		}
	}
	return nil
}

func updateVeleroObjectStore(ctx context.Context, controllers types.ControllerConfig, logs chan<- string, accessKey string, secretKey string, hostname string, endpoint string, originalSecretKey string, originalHostname string) error {
	client := controllers.Client
	restartVelero := false
	// if kubernetes_resource_exists velero backupstoragelocation default
	veleroBSL, err := controllers.VeleroV1.BackupStorageLocations("velero").Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("get velero backupstoragelocation in velero namespace: %v", err)
		}
	}
	if veleroBSL != nil {
		veleroS3URL, ok := veleroBSL.Spec.Config["s3Url"]
		if ok && strings.Contains(veleroS3URL, originalHostname) {
			if logs != nil {
				logs <- "Updating Velero backup locations to use new object store"
			}

			restartVelero = true

			veleroBSL.Spec.Config["s3Url"] = hostname
			veleroBSL.Spec.Config["publicUrl"] = fmt.Sprintf("http://%s/", endpoint)

			_, err = controllers.VeleroV1.BackupStorageLocations("velero").Update(ctx, veleroBSL, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("update velero backupstoragelocation in velero namespace: %v", err)
			}

			// update each restic repository
			backupRepos, err := controllers.VeleroV1.BackupRepositories("velero").List(ctx, metav1.ListOptions{LabelSelector: "velero.io/storage-location=default"})
			if err != nil {
				return fmt.Errorf("list velero backuprepositories in velero namespace: %v", err)
			}
			for _, backupRepo := range backupRepos.Items {
				backupRepo.Spec.ResticIdentifier = strings.ReplaceAll(backupRepo.Spec.ResticIdentifier, originalHostname, hostname)
				_, err = controllers.VeleroV1.BackupRepositories("velero").Update(ctx, &backupRepo, metav1.UpdateOptions{})
				if err != nil {
					return fmt.Errorf("update velero backuprepository %s in velero namespace: %v", backupRepo.Name, err)
				}
			}
		}
	}

	// if kubernetes_resource_exists velero secret cloud-credentials
	veleroSecret, err := client.CoreV1().Secrets("velero").Get(ctx, "cloud-credentials", metav1.GetOptions{})
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("get cloud-credentials secret in velero namespace: %v", err)
		}
	}
	if veleroSecret != nil {
		cloud, ok := veleroSecret.Data["cloud"]

		// check if this is using the original secret key and thus should be updated
		// if it's a custom endpoint, we don't want to overwrite it
		if ok && strings.Contains(string(cloud), originalSecretKey) {
			if logs != nil {
				logs <- "Updating Velero cloud credentials to use new object store"
			}

			restartVelero = true

			// update 'cloud' by replacing the aws access key and secret key
			cloudString := regexp.MustCompile(`aws_access_key_id=.*`).ReplaceAllString(string(cloud), fmt.Sprintf("aws_access_key_id=%s", accessKey))
			cloudString = regexp.MustCompile(`aws_secret_access_key=.*`).ReplaceAllString(cloudString, fmt.Sprintf("aws_secret_access_key=%s", secretKey))

			veleroSecret.Data["cloud"] = []byte(cloudString)

			_, err = client.CoreV1().Secrets("velero").Update(ctx, veleroSecret, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("update cloud-credentials secret in velero namespace: %v", err)
			}
		}
	}

	if restartVelero {
		if logs != nil {
			logs <- "Restarting Velero's restic daemonset"
		}

		err = util.RestartDaemonSet(ctx, client, "velero", "restic")
		if err != nil {
			return fmt.Errorf("restart velero restic daemonset after migrating object store: %v", err)
		}

		if logs != nil {
			logs <- "Restic daemonset restarted"
			logs <- "Restarting Velero"
		}

		err = util.RestartDeployment(ctx, client, "velero", "velero")
		if err != nil {
			return fmt.Errorf("restart velero deployment after migrating object store: %v", err)
		}

		if logs != nil {
			logs <- "Velero restarted"
		}
	}
	return nil
}

func syncBucket(ctx context.Context, src *minio.Client, dst *minio.Client, bucket string, logs chan<- string) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	count := 0

	exists, err := dst.BucketExists(ctx, bucket)
	if err != nil {
		return count, fmt.Errorf("failed to check if bucket %q exists in destination: %v", bucket, err)
	}
	if !exists {
		if err := dst.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return count, fmt.Errorf("failed to make bucket %q in destination: %v", bucket, err)
		}
	}

	srcObjectInfoChan := src.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true})

	for srcObjectInfo := range srcObjectInfoChan {
		srcObject, err := src.GetObject(ctx, bucket, srcObjectInfo.Key, minio.GetObjectOptions{})
		if err != nil {
			return count, fmt.Errorf("get %s from source: %v", srcObjectInfo.Key, err)
		}

		if logs != nil {
			logs <- fmt.Sprintf("  - %s", srcObjectInfo.Key)
		}

		_, err = dst.PutObject(ctx, bucket, srcObjectInfo.Key, srcObject, srcObjectInfo.Size, minio.PutObjectOptions{
			ContentType:     srcObjectInfo.ContentType,
			ContentEncoding: srcObjectInfo.Metadata.Get("Content-Encoding"),
		})
		_ = srcObject.Close()
		if err != nil {
			return count, fmt.Errorf("failed to copy object %s to destination: %v", srcObjectInfo.Key, err)
		}

		count++
	}

	return count, nil
}