package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/replicatedhq/ekco/pkg/cluster"
	"github.com/replicatedhq/ekco/pkg/ekcoops"
	"github.com/replicatedhq/ekco/pkg/migrate"
)

func Serve(ctx context.Context, config ekcoops.Config, client *cluster.Controller) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/storagemigration/cluster-ready", func(w http.ResponseWriter, r *http.Request) {
		status, err := migrate.IsClusterReady(r.Context(), config, client.Config)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, err := w.Write([]byte(err.Error()))
			if err != nil {
				log.Printf("failed to write server response error: %v", err)
			}
			return
		}
		data, err := json.Marshal(status)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if _, err := w.Write([]byte(err.Error())); err != nil {
				log.Printf("failed to write json marshaling error: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err = w.Write(data); err != nil {
			log.Printf("failed to write cluster-ready status response: %v", err)
		}
	})

	mux.HandleFunc("/storagemigration/ready", func(w http.ResponseWriter, r *http.Request) {
		status, err := migrate.IsMigrationReady(r.Context(), config, client.Config)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, err := w.Write([]byte(err.Error()))
			if err != nil {
				log.Printf("get ready status: %v", err)
			}
			return
		}
		data, err := json.Marshal(status)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if _, err := w.Write([]byte(err.Error())); err != nil {
				log.Printf("write json marshaling error: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err = w.Write(data); err != nil {
			log.Printf("write ready status: %v", err)
		}
	})

	mux.HandleFunc("/storagemigration/status", func(w http.ResponseWriter, r *http.Request) {
		status, err := migrate.GetMigrationStatus(r.Context(), client.Config)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_, err := w.Write([]byte(err.Error()))
			if err != nil {
				log.Printf("failed to write migration status error: %v", err)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		_, err = w.Write([]byte(status))
		if err != nil {
			log.Printf("failed to write api response for /storagemigration/status: %v", err)
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

	server := &http.Server{Addr: ":8080", Handler: mux}
	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	return server.ListenAndServe()
}
