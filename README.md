# Embedded kURL cluster operator (EKCO)

EKCO is responsible for performing various operations to maintain the health of a kURL cluster.

[Documentation](https://kurl.sh/docs/add-ons/ekco)

## Test manually

### Deprecated
```bash
make docker-image
kubectl apply -k deploy/
```

This method is very much out of sync and doesn't work.  The current best way to test is to deploy a kurl cluster and
modify the deployment to pull the docker container produced as part of development.

Steps
1. **make build-ttl.sh** - Build the docker container for the current development environment and deploy it to ttl.sh
2. Deploy a kurl cluster that includes ecko and any other requirements for testing.
3. **kubectl edit -n kurl deployment/ekc-operator**
   1. Replace .spec.image with your ttl.sh image
   2. Replace .spec.imagePullPolicy with "Always"
   3. **kubectl delete pod -l app=ekc-operator -n kurl** - Delete the pod in the deployment to pull the new image

### Automated testing
The `scripts/e2e-test.sh` script automates the above manual workflow using [Replicated CMX](https://docs.replicated.com/vendor/testing-about). It provisions a CMX VM, installs a kURL cluster, patches the EKCO deployment with a ttl.sh image, and runs health checks. Run `make test-e2e` after `make build-ttl.sh` to use it.


## Release

Releases are automated with [release-please](https://github.com/googleapis/release-please).

### How it works

1. When a PR with a [Conventional Commit](https://www.conventionalcommits.org/) message is merged to `main`, the `release-please` workflow opens (or updates) a release PR.
2. The release PR bumps the version in `.release-please-manifest.json`, updates `CHANGELOG.md`, and proposes the next semantic version based on commit types.
3. Merging the release PR triggers `release-please` to create a Git tag (e.g., `v0.29.0`) and a GitHub release.
4. The `release-please` workflow then builds and pushes the Docker image `replicated/ekco:<tag>` automatically.

### Manual release (if needed)

If you must bypass the automated flow, push a tag in the format `v[0-9]+\.[0-9]+\.[0-9]+(-[0-9a-z-]+)?`:

```bash
git tag -a v0.1.0 -m "Release v0.1.0" && git push origin v0.1.0
```

This triggers the [`release`](.github/workflows/release.yaml) workflow, which builds and pushes the Docker image.
