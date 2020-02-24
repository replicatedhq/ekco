# Embedded Kurl cluster operator (ekco)

Cluster operator for embedded Kurl clusters.

## Test manually

```bash
make docker-image
kubectl apply -k deploy/
```

## Release

To make a new release push a tag in the format `vYYYY.MM.DD-[0-9]`.

```bash
git tag -a v2020.01.28-0 -m "Release v2020.01.28-0" && git push origin v2020.01.28-0
```
