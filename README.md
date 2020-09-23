# Embedded kURL cluster operator (EKCO)

EKCO is responsible for performing various operations to maintain the health of a kURL cluster.

[Documentation](https://kurl.sh/docs/add-ons/ekco)

## Test manually

```bash
make docker-image
kubectl apply -k deploy/
```

## Release

To make a new release push a tag in the format `v[0-9]+\.[0-9]+\.[0-9]+(-[0-9a-z-]+)?`.

```bash
git tag -a v0.1.0 -m "Release v0.1.0" && git push origin v0.1.0
```
