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
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/storagemigration/ready", func(w http.ResponseWriter, r *http.Request) {
		_, message, err := migrate.IsMigrationReady(context.TODO(), config, client.Config)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, err := w.Write([]byte(err.Error()))
			if err != nil {
				log.Printf("get ready status: %v", err)
			}
			return
		}

		w.WriteHeader(http.StatusOK)
		_, err = w.Write([]byte(message))
		if err != nil {
			log.Printf("write ready status: %v", err)
		}
	})

	mux.HandleFunc("/storagemigration/status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(migrate.GetMigrationStatus()))
		if err != nil {
			log.Printf("write status: %v", err)
		}
	})

	mux.HandleFunc("/storagemigration/logs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := w.Write([]byte(migrate.GetMigrationLogs()))
		if err != nil {
			log.Printf("write logs: %v", err)
		}
	})

	mux.HandleFunc("/storagemigration/approve", func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer "+config.StorageMigrationAuthToken && config.StorageMigrationAuthToken != "" {
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

	err := http.ListenAndServe(":8080", mux)
	if err != nil {
		log.Fatalf("server exited: %v", err)
	}
}
