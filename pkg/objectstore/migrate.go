package objectstore

import (
	"context"
	"fmt"
	"regexp"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
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

		numObjects, err := syncBucket(ctx, minioClient, rookClient, bucket.Name)
		if err != nil {
			return fmt.Errorf("failed to sync bucket %s: %v", bucket.Name, err)
		}

		if logs != nil {
			logs <- fmt.Sprintf("Copied %d objects in %s", numObjects, bucket.Name)
		}
	}

	return nil
}

// UpdateConsumers updates the access key and secret key for all consumers of the object store.
// it handles kotsadm, registry, and velero.
func UpdateConsumers(ctx context.Context, client kubernetes.Interface, endpoint, hostname, accessKey, secretKey string) error {
	// if kubernetes_resource_exists default secret kotsadm-s3
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

		_, err = client.CoreV1().Secrets("default").Update(ctx, kotsadmS3, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("update kotsadm-s3 secret in default namespace: %v", err)
		}

		err = util.RestartDeployment(ctx, client, "default", "kotsadm")
		if err != nil {
			return fmt.Errorf("restart kotsadm deployment after migrating object store: %v", err)
		}
		err = util.RestartStatefulset(ctx, client, "default", "kotsadm")
		if err != nil {
			return fmt.Errorf("restart kotsadm statefulset after migrating object store: %v", err)
		}
	}

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
		existingConfig := registryConfig.Data["config.yml"]
		newConfig := regexp.MustCompile(`regionendpoint: http.*`).ReplaceAllString(existingConfig, fmt.Sprintf("regionendpoint: http://%s/", endpoint))
		registryConfig.Data["config.yml"] = newConfig

		_, err = client.CoreV1().ConfigMaps("kurl").Update(ctx, registryConfig, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("update registry-config configmap in kurl namespace: %v", err)
		}

		if registrySecret.StringData == nil {
			registrySecret.StringData = map[string]string{}
		}

		registrySecret.StringData["access-key-id"] = accessKey
		registrySecret.StringData["secret-access-key"] = secretKey
		registrySecret.StringData["object-store-cluster-ip"] = endpoint
		registrySecret.StringData["object-store-hostname"] = hostname

		_, err = client.CoreV1().Secrets("kurl").Update(ctx, registrySecret, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("update registry-s3-secret secret in kurl namespace: %v", err)
		}

		err = util.RestartDeployment(ctx, client, "kurl", "registry")
		if err != nil {
			return fmt.Errorf("restart registry deployment after migrating object store: %v", err)
		}
	}

	//TODO if kubernetes_resource_exists velero backupstoragelocation default

	//TODO if kubernetes_resource_exists velero secret cloud-credentials

	return nil
}

func syncBucket(ctx context.Context, src *minio.Client, dst *minio.Client, bucket string) (int, error) {
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
