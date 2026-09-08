# Testing EKCO kubelet-serving CSR approval

This guide exercises `reconcileCertificateSigningRequests` in a real kURL cluster by forcing a kubelet serving certificate rotation. It is useful for validating both the fix for [GHSA-x7j5-fww5-mqmv](https://github.com/replicatedhq/ekco/security/advisories/GHSA-x7j5-fww5-mqmv) and the regression test in `pkg/ekcoops/csr_test.go`.

## What this tests

- EKCO sees a new `kubernetes.io/kubelet-serving` CSR.
- The operator’s approval logic runs against an authenticated, real-world request from a kubelet.
- A valid CSR from the node itself is approved and the kubelet gets a new serving certificate.
- After the fix, malicious CSRs (wrong requester, foreign SANs, etc.) are rejected.

## Prerequisites

- A kURL cluster with EKCO running (`kubectl get pods -n kurl -l app=ekc-operator`).
- `kubectl` access to the cluster.
- `ssh` or shell access to a worker node.
- A maintenance window where brief `kubectl logs` / `kubectl exec` disruption to one worker is acceptable.

## Procedure

### 1. Pick a worker node

Do **not** run this on a control-plane node first. Choose a worker node and record its name.

```bash
export NODE_NAME=worker-1
```

### 2. Optional: drain the node

```bash
kubectl drain "$NODE_NAME" --ignore-daemonsets --delete-emptydir-data
```

### 3. Remove the kubelet serving certificate on the node

SSH to the node and delete the kubelet serving certificate/key. The kubelet will recreate them on restart and submit a new CSR.

```bash
ssh "$NODE_NAME" sudo bash -c '
  ls -la /var/lib/kubelet/pki/kubelet-server*
  rm -f /var/lib/kubelet/pki/kubelet-server-*.pem
  rm -f /var/lib/kubelet/pki/kubelet-server-current.pem
'
```

### 4. Restart kubelet

```bash
ssh "$NODE_NAME" sudo systemctl restart kubelet
```

If kubelet runs as a container, restart the container instead:

```bash
ssh "$NODE_NAME" sudo bash -c '
  KUBELET_PID=$(pgrep -x kubelet)
  kill -SIGHUP "$KUBELET_PID"
'
```

### 5. Watch for a new CSR

From a control-plane node or your workstation:

```bash
kubectl get csr -w
```

Expect a CSR that looks like:

```text
NAME        AGE   SIGNERNAME                        REQUESTOR              STATUS
worker-1... 0s    kubernetes.io/kubelet-serving     system:node:worker-1   Pending
```

The `REQUESTOR` must be `system:node:<node-name>` and the `SIGNERNAME` must be `kubernetes.io/kubelet-serving`.

### 6. Watch EKCO approve the CSR

```bash
kubectl logs -n kurl deployment/ekc-operator -f
```

With the current vulnerable code you will see:

```text
CSR approval is successful <csr-name>
```

After the fix, the same message should appear only for a valid CSR that passes the new validation checks.

### 7. Verify the kubelet serving certificate was renewed

Back on the worker node:

```bash
ssh "$NODE_NAME" sudo bash -c '
  ls -la /var/lib/kubelet/pki/kubelet-server*
  openssl x509 -in /var/lib/kubelet/pki/kubelet-server-current.pem -text -noout | head -20
'
```

You should see a new certificate issued by the cluster CA, with SANs matching the node’s addresses.

### 8. Verify `kubectl logs`/`exec` work

```bash
kubectl run -it --rm debug --image=alpine --overrides='{"spec":{"nodeName":"'$NODE_NAME'"}}' --restart=Never -- sh
```

Inside the pod, run `hostname` and then exit. The command should succeed, which proves the kubelet serving cert is trusted by the API server.

### 9. Uncordon the node

```bash
kubectl uncordon "$NODE_NAME"
```

## What can go wrong

- If EKCO is disabled or the add-on does not grant `approve` on the `kubernetes.io/kubelet-serving` signer, the CSR stays `Pending` and kubelet logs errors. Check EKCO RBAC and the `auto_approve_kubelet_csrs` configuration.
- If the kubelet does not restart cleanly, the node may become `NotReady`. Use `journalctl -u kubelet -f` on the node to debug.
- If you test on a control-plane node and the kubelet serving cert is not approved quickly, `kubectl logs`/`exec` to pods on that node can break until the cert is restored.

## Testing rejection of malicious CSRs

To verify the fix rejects attacker-shaped CSRs, create a test CSR manually and submit it:

```bash
openssl req -new -newkey rsa:2048 -nodes \
  -subj "/O=system:nodes/CN=system:node:worker-1" \
  -addext "subjectAltName = DNS:kubernetes,DNS:kubernetes.default,IP:10.96.0.1" \
  -keyout /tmp/test.key -out /tmp/test.csr

CSR_B64=$(base64 -w 0 /tmp/test.csr)

cat <<EOF | kubectl apply -f -
apiVersion: certificates.k8s.io/v1
kind: CertificateSigningRequest
metadata:
  name: test-attack-csr
spec:
  signerName: kubernetes.io/kubelet-serving
  request: ${CSR_B64}
  usages:
  - digital signature
  - key encipherment
  - server auth
  username: system:serviceaccount:default:pwn
EOF
```

Then watch:

```bash
kubectl get csr test-attack-csr -w
```

After the fix, this CSR should remain `Pending` and EKCO should not log an approval. Clean it up when done:

```bash
kubectl delete csr test-attack-csr
```

## Related code

- `pkg/ekcoops/operator.go` — `reconcileCertificateSigningRequests`
- `pkg/ekcoops/csr_test.go` — unit/regression tests
- `../kURL/addons/ekco/template/base/configmap.tmpl.yaml` — add-on setting `auto_approve_kubelet_csrs: true`
- `../kURL/addons/ekco/template/base/rbac.yaml` — signer approval RBAC
