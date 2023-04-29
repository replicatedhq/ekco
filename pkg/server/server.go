package server

import (
	"context"
	"log"
	"net/http"

	"github.com/replicatedhq/ekco/pkg/cluster"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	"github.com/replicatedhq/ekco/pkg/migrate"
)

func Serve(config ekcoops.Config, client *cluster.Controller) {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("/storagemigration/ready", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, message, err := migrate.IsMigrationReady(context.TODO(), config, client.Config)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, err := w.Write([]byte(err.Error()))
			if err != nil {
				log.Printf("get ready status: %v", err)
			}
		}

		_, err = w.Write([]byte(message))
		if err != nil {
			log.Printf("write ready status: %v", err)
		}
	})

	http.HandleFunc("/storagemigration/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(migrate.GetMigrationStatus()))
		if err != nil {
			log.Printf("write status: %v", err)
		}
	})

	http.HandleFunc("/storagemigration/logs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(migrate.GetMigrationLogs()))
		if err != nil {
			log.Printf("write logs: %v", err)
		}
	})

	http.HandleFunc("/storagemigration/approve", func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer "+config.StorageMigrationAuthToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, err := w.Write([]byte("UNAUTHORIZED"))
			if err != nil {
				log.Printf("write unauthorized: %v", err)
			}
			return
		}

		go migrate.ObjectStorageAndPVCs(config, client.Config)

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
