package server

import (
	"context"
	"fmt"
	"k8s.io/client-go/kubernetes"
	"log"
	"net/http"
	"sync"

	"github.com/replicatedhq/ekco/pkg/ekcoops"
	"github.com/replicatedhq/pvmigrate/pkg/migrate"
)

const (
	MIGRATION_STATUS_RUNNING   = "running"
	MIGRATION_STATUS_FAILED    = "failed"
	MIGRATION_STATUS_COMPLETED = "completed"
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
		_, err := w.Write([]byte("TODO"))
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

	fileLog := log.New(nil, "", 0) // TODO: save to logs string

	options := migrate.Options{
		SourceSCName: "scaling",
		DestSCName:   config.RookStorageClass,
		RsyncImage:   config.MinioUtilImage,
		SetDefaults:  true,
	}

	migrationStatus = MIGRATION_STATUS_RUNNING

	err := migrate.Migrate(context.TODO(), fileLog, client, options)
	if err != nil {
		migrationStatus = fmt.Sprintf("%s: %v", MIGRATION_STATUS_FAILED, err)
		return
	}

	// TODO: delete the scaling storage class
	// TODO: run object storage migration

	migrationStatus = MIGRATION_STATUS_COMPLETED
}
