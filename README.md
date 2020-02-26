# Embedded Kurl cluster operator (EKCO)

EKCO is responsible for performing various operations to maintain the health of a Kurl cluster.

### Purge nodes

In an HA Kubernetes cluster the EKCO operator will automatically purge failed nodes that have been unreachable for more than `node_unreachable_toleration` (default 1h). The following steps will be taken during a purge:

1. Delete the Deployment resource for the OSD from the rook-ceph namespace
1. Exec into the Rook operator pod and run the command `ceph osd purge <id>`
1. Delete the Node resource
1. Remove the node from the CephCluster resource named rook-ceph in the rook-ceph namespace unless storage is managed automatically with `useAllNodes: true`
1. (Masters only) Connect to the etcd cluster and remove the peer
1. (Masters only) Remove the apiEndpoint for the node from the kubeadm-config ConfigMap in the kube-system namespace

### Rook

The EKCO operator is responsible for appending nodes to the CephCluster `storage.nodes` setting to include the node in the list of nodes used by Ceph for storage. This operation will only append nodes. Removing nodes is done during purge.

EKCO is also responsible for adjusting the Ceph block pool, filesystem and object store replication factor up and down in accordance with the size of the cluster from `min_ceph_pool_replication` (default 1) to `max_ceph_pool_replication` (default 3).

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
