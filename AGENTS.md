# EKCO — Agent Guide

Compact, repo-specific guidance for OpenCode sessions.

## What this is

EKCO (Embedded kURL cluster operator) is a single Go binary that runs as a Kubernetes operator to maintain the health of a kURL cluster. It is **not** a library or multi-service repo.

- **Language / toolchain:** Go (module `github.com/replicatedhq/ekco`)
- **Entrypoint:** `cmd/ekco/main.go`
- **CLI framework:** Cobra / Viper (commands live in `cmd/ekco/cli/`)
- **Primary command:** `ekco operator` (long-running operator)
- **Other commands:** `purge-node`, `rotate-certs`, `regen-cert`, `change-load-balancer`, `generate-haproxy-*`, `set-kubeconfig-server` (see `cmd/ekco/cli/`)

## Build, test, lint

Use the Makefile. Do not guess the Go version or linter config.

```bash
# Install tooling (golangci-lint)
make deps

# Full verification — order enforced by Makefile: lint -> vet -> test
make test

# Build binary (outputs bin/ekco)
make build

# Build Docker image
make docker-image
```

**Lint rules are non-default.** `.golangci.yaml` disables `errcheck` and `staticcheck`, and ignores all `*_test.go` files (`exclusions.paths`). Do not re-enable them locally.

**Test scope:** `go test ./pkg/... ./cmd/...` — only `pkg` and `cmd`; there is no root-level test target.

## Docker / deploy quirks

- The Dockerfile is at `deploy/Dockerfile`, not the repo root.
- `ROOK_VERSION` defaults to `1.11.8` in the Makefile and Dockerfile.
- The build stage downloads Helm and pulls `rook-ceph-cluster` chart into `pkg/helm/charts`.
- Git SHA / version are injected via ldflags at build time (`pkg/version`).

## Manual integration testing

Do **not** use `make docker-image && kubectl apply -k deploy/` — the README notes this is broken and out of sync.

Current workflow (from README):

1. `make build-ttl.sh` — pushes a temporary image to `ttl.sh/${USER}/ekco:12h`.
2. Deploy a kURL cluster that includes EKCO.
3. Patch the deployment:
   ```bash
   kubectl edit -n kurl deployment/ekc-operator
   # set image to ttl.sh/<user>/ekco:12h and imagePullPolicy: Always
   kubectl delete pod -l app=ekc-operator -n kurl
   ```

## Mocks

Generated with `github.com/golang/mock`:

```bash
make generate-mocks
```

This currently only covers `pkg/k8s/exec.go` → `pkg/k8s/mock/mock_exec.go`.

## Monorepo boundaries

There are none. The repo is a single Go module with two top-level directories:

- `cmd/` — CLI commands and `main.go`
- `pkg/` — operator logic, cluster controller, K8s clients, webhooks, cert rotation, object-store helpers, etc.
- `deploy/` — Dockerfile, Kustomize manifests, and RBAC for the operator

No frontend, no separate API service, no nested Go modules.

## Key external dependencies & constraints

- Deep integration with **Rook Ceph**, **Kubernetes**, **Contour**, **Velero**, **etcd**, and **Prometheus Operator**.
- `go.mod` carries a large set of `replace` and `exclude` blocks inherited from Rook. Do not prune them; they resolve transitive conflicts.
- CI uses `go-version-file: 'go.mod'` so the declared Go version is the source of truth.

## CI / release conventions

- **PRs:** `make deps test build` + `make docker-image` (pushed to `ttl.sh`).
- **Main branch:** `make docker-image` pushed to `replicated/ekco:alpha`.
- **Releases:** Push a semver tag `v*.*.*` (e.g. `git tag -a v0.1.0 -m "Release v0.1.0" && git push origin v0.1.0`). Image is pushed to `replicated/ekco:<tag>`.
- Scheduled vulnerability scans use Grype (`.grype.yaml`) against the repo and the built image.

## When in doubt

- Prefer `Makefile` targets over raw `go` commands — ldflags and build args matter.
- Trust executable configs (`Makefile`, `.golangci.yaml`, `.github/workflows/`) over prose in README for build/test steps.
