# SecureBuild packaging

This directory owns the Melange package and APKO image definitions for ekco.
SecureBuild reads them from a release tag, builds APKs for x86_64 and aarch64,
then builds the image after both APKs are available. The normal Docker Hub
release remains available.

## Admin setup

Configure the following records in SecureBuild before the first release with
these files. Both records must use the identical Git remote URL.

| Setting | Value |
| --- | --- |
| Package family name | `ekco` |
| Package name template | `{name}-{major}.{minor}` |
| Git remote | `https://github.com/replicatedhq/ekco` |
| Melange path | `securebuild/package/ekco/melange.yaml` |
| Initial tag | First stable release containing these files |
| Automatic monitoring | Disabled; release CI triggers builds |
| Image name | `ekco` |
| APKO path | `securebuild/image/apko-ekco.yaml` |
| Image test path | `securebuild/image/apko-ekco.test.yaml` |
| Image tag template | `{major}.{minor}.{patch}` |

Add `SECUREBUILD_API_TOKEN` as a repository Actions secret using the team's
SecureBuild service account. The release workflow calls `publish-securebuild.yml`
after the Docker Hub release succeeds. It also supports manual dispatch for
retrying a release. The CLI waits for the package build and APK publication
before building the image, adding the upstream `v`-prefixed image tag as an alias.

The version/name in the Melange file are bootstrap defaults; SecureBuild replaces
them from the selected release tag and family template. It pins the APKO's `ekco`
dependency to that release version. Dependency updates and rebuilds are managed
in SecureBuild. Its export to `securebuild-specs` is generated output: make
catalog changes through SecureBuild instead of editing the export.

## Existing releases

Tags created before these files were added cannot use this workflow. For
`v0.28.15`, create the `ekco-0.28` package at version `0.28.15` and an image with
`ekco~0.28.15` through SecureBuild. Keep that release tag unchanged. Link the
existing image and family to this repository when the first release containing
the definitions is available.

## Compatibility and validation

The package uses the upstream Makefile, a static Go build, and the Rook Ceph
cluster chart version used by `deploy/Dockerfile`. The chart is downloaded with
a SHA256 check before Go embeds it. When changing `ROOK_VERSION`, update the
chart version and checksum in the Melange definition too. Grump patches Go
dependency vulnerabilities before compilation.

The image preserves `/usr/bin/ekco` as its entrypoint and runs as root because
host-task pods write mounted host configuration. Bash and filesystem utilities
are included for these pods. Version and commit metadata are embedded in the
binary; the generic image definition does not set upstream's per-release
`VERSION` and `GIT_SHA` environment variables.

Run both `melange build` and `melange test` when changing the package. Image tests
compare upstream configuration and version output and exercise the shell-based
host-task commands. Use `replicated/ekco:<release-tag>` as the reference image.

For cluster validation, use the existing `scripts/e2e-test.sh` with `EKCO_IMAGE`
set to the SecureBuild image. Also set ekco's `host_task_image` and
`rotate_certs_image` configuration to that image and exercise these tasks:
changing only the operator image leaves these defaults at
`replicated/ekco:latest`.
