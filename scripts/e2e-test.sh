#!/usr/bin/env bash
set -euo pipefail

# E2E test script for EKCO on a kURL cluster in CMX.
# Usage:
#   EKCO_IMAGE=ttl.sh/user/ekco:tag ./scripts/e2e-test.sh
#
# Environment variables:
#   REPLICATED_APP        - Replicated app slug (default: kurl-sandbox)
#   REPLICATED_API_TOKEN  - Replicated API token (required)
#   EKCO_IMAGE            - EKCO Docker image to test (required)
#   VM_TTL                - VM TTL (default: 2h)
#   VM_DISTRIBUTION       - VM distribution (default: ubuntu)
#   VM_VERSION            - VM version (default: 24.04)
#   VM_INSTANCE_TYPE      - VM instance type (default: r1.medium)
#   VM_DISK               - VM disk size in GiB (default: 100)
#   SSH_KEY               - Path to SSH private key (default: ~/.ssh/id_rsa)
#   KURL_INSTALLER_ID     - kURL installer ID (default: fetched from app)
#   SKIP_KURL_INSTALL     - Set to '1' to skip kURL install (for debugging)

REPLICATED_APP="${REPLICATED_APP:-kurl-sandbox}"
REPLICATED_TOKEN="${REPLICATED_API_TOKEN:-}"
EKCO_IMAGE="${EKCO_IMAGE:-}"
VM_TTL="${VM_TTL:-2h}"
VM_DISTRIBUTION="${VM_DISTRIBUTION:-ubuntu}"
VM_VERSION="${VM_VERSION:-22.04}"
VM_INSTANCE_TYPE="${VM_INSTANCE_TYPE:-r1.large}"
VM_DISK="${VM_DISK:-100}"
SSH_KEY="${SSH_KEY:-$HOME/.ssh/id_rsa}"
# Expand a leading tilde manually in case the caller passed a literal '~' path.
SSH_KEY="${SSH_KEY/#\~/$HOME}"
KURL_INSTALLER_ID="${KURL_INSTALLER_ID:-}"
SKIP_KURL_INSTALL="${SKIP_KURL_INSTALL:-0}"
LOG_DIR="${LOG_DIR:-/tmp}"

if [[ -z "$EKCO_IMAGE" ]]; then
    echo "ERROR: EKCO_IMAGE environment variable is required"
    exit 1
fi

if [[ -z "$REPLICATED_TOKEN" ]]; then
    echo "ERROR: REPLICATED_API_TOKEN environment variable is required"
    exit 1
fi

if ! command -v replicated &> /dev/null; then
    echo "ERROR: replicated CLI is required"
    exit 1
fi

if ! command -v jq &> /dev/null; then
    echo "ERROR: jq is required"
    exit 1
fi

# Ensure SSH key has a username comment for replicated CLI
if [[ ! -f "${SSH_KEY}.pub" ]]; then
    echo "Generating SSH key for CMX VM access..."
    ssh-keygen -t ed25519 -N '' -f "$SSH_KEY" -C "replicated"
fi

# Check if the public key has a comment (required by replicated CLI)
PUB_KEY_COMMENT=$(awk '{print $3}' "${SSH_KEY}.pub")
if [[ -z "$PUB_KEY_COMMENT" ]]; then
    echo "SSH public key has no comment; generating a new key with username comment..."
    mv "$SSH_KEY" "${SSH_KEY}.bak"
    mv "${SSH_KEY}.pub" "${SSH_KEY}.pub.bak"
    ssh-keygen -t ed25519 -N '' -f "$SSH_KEY" -C "replicated"
fi

# Generate a unique tag for this test run
TEST_TAG="ekco-e2e-$(date +%s)"
VM_NAME="$TEST_TAG"

echo "=== EKCO E2E Test ==="
echo "App: $REPLICATED_APP"
echo "EKCO Image: $EKCO_IMAGE"
echo "VM: $VM_DISTRIBUTION $VM_VERSION ($VM_INSTANCE_TYPE, ${VM_DISK}GiB)"
echo "TTL: $VM_TTL"
echo ""

# Cleanup function - always run on exit
cleanup() {
    local exit_code=$?
    echo ""
    echo "=== Cleanup ==="
    
    if [[ -n "${VM_ID:-}" ]]; then
        echo "Removing VM $VM_ID..."
        replicated --token "$REPLICATED_TOKEN" vm rm "$VM_ID" 2>/dev/null || true
    fi
    
    if [[ $exit_code -ne 0 ]]; then
        echo "ERROR: E2E test failed with exit code $exit_code"
    else
        echo "SUCCESS: E2E test completed"
    fi
}
trap cleanup EXIT

# Fetch kURL installer ID if not provided
if [[ -z "$KURL_INSTALLER_ID" ]]; then
    echo "=== Fetching kURL installer ID ==="

    INSTALLER_JSON=$(replicated --token "$REPLICATED_TOKEN" --app "$REPLICATED_APP" installer ls --output json 2>&1) || {
        echo "ERROR: failed to list installers for app '$REPLICATED_APP'"
        echo "Listing accessible apps..."
        replicated --token "$REPLICATED_TOKEN" app ls --output json 2>&1 || true
        echo "Hint: verify REPLICATED_API_TOKEN has access to the '$REPLICATED_APP' app."
        exit 1
    }

    # Default to the minimal EKCO test installer (sequence 2) for reliability
    KURL_INSTALLER_ID=$(echo "$INSTALLER_JSON" | jq -r '.[] | select(.sequence == 2) | .kurlInstallerID' || true)
    if [[ -z "$KURL_INSTALLER_ID" || "$KURL_INSTALLER_ID" == "null" ]]; then
        # Fallback to any available installer
        KURL_INSTALLER_ID=$(echo "$INSTALLER_JSON" | jq -r '.[0].kurlInstallerID' || true)
    fi
    if [[ -z "$KURL_INSTALLER_ID" || "$KURL_INSTALLER_ID" == "null" ]]; then
        echo "ERROR: Could not find kURL installer for app '$REPLICATED_APP'"
        exit 1
    fi
    echo "Using kURL installer: $KURL_INSTALLER_ID"
fi

# Create VM
echo ""
echo "=== Creating CMX VM ==="
VM_JSON=$(replicated --token "$REPLICATED_TOKEN" vm create \
    --distribution "$VM_DISTRIBUTION" \
    --version "$VM_VERSION" \
    --instance-type "$VM_INSTANCE_TYPE" \
    --disk "$VM_DISK" \
    --ttl "$VM_TTL" \
    --name "$VM_NAME" \
    --wait 10m \
    --output json \
    --ssh-public-key "${SSH_KEY}.pub" 2>&1)

# Filter out "Update available" messages before parsing JSON
VM_JSON_CLEAN=$(echo "$VM_JSON" | grep -v "Update available" | grep -v "To upgrade")
echo "$VM_JSON_CLEAN"
VM_ID=$(echo "$VM_JSON_CLEAN" | jq -r '.[0].id')
VM_IP=$(echo "$VM_JSON_CLEAN" | jq -r '.[0].direct_ssh_endpoint')

VM_SSH_PORT=$(echo "$VM_JSON_CLEAN" | jq -r '.[0].direct_ssh_port')

echo ""
echo "VM created: $VM_ID ($VM_IP:$VM_SSH_PORT)"

# Derive the SSH username from the key comment (portion before the first '@').
# The CMX VM creates a Linux user based on the public key comment.
SSH_USER=$(awk '{print $3}' "${SSH_KEY}.pub" | cut -d'@' -f1)
if [[ -z "$SSH_USER" ]]; then
    echo "Warning: could not determine SSH user from key comment; falling back to 'ubuntu'"
    SSH_USER="ubuntu"
fi
SSH_TARGET="${SSH_USER}@${VM_IP}"
echo "Using SSH target: $SSH_TARGET (port $VM_SSH_PORT)"

SSH_OPTS=(-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -i "$SSH_KEY")

# Wait for SSH to be ready (max 10 min)
echo ""
echo "=== Waiting for SSH ==="
SSH_READY=0
for i in {1..60}; do
    if ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" "echo ok" > /tmp/ssh_wait.log 2>&1; then
        echo "SSH is ready"
        SSH_READY=1
        break
    fi
    echo "Waiting for SSH... ($i/60)"
    if [[ $i -ge 55 ]]; then
        echo "SSH diagnostics:"
        cat /tmp/ssh_wait.log || true
    fi
    sleep 10
done

if [[ $SSH_READY -eq 0 ]]; then
    echo "ERROR: SSH did not become ready within timeout"
    echo "Last SSH attempt output:"
    cat /tmp/ssh_wait.log || true
    exit 1
fi

# Verify SSH works
ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" "whoami" > /dev/null || {
    echo "ERROR: Could not establish SSH connection"
    exit 1
}

# Install kURL (unless skipped for debugging)
if [[ "$SKIP_KURL_INSTALL" != "1" ]]; then
    echo ""
    echo "=== Installing host dependencies ==="
    ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
        "sudo apt-get update -qq && sudo apt-get install -y -qq containerd iptables conntrack" || {
            echo "WARNING: Some host packages may already be present or installation partially failed"
        }

    echo "=== Installing kURL (this may take 20-30 minutes) ==="
    ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
        "curl -fsSL https://kurl.sh/$KURL_INSTALLER_ID | sudo bash" || {
            echo "ERROR: kURL installation failed"
            exit 1
        }
    echo ""
    echo "=== kURL installation complete ==="
else
    echo ""
    echo "=== Skipping kURL install (SKIP_KURL_INSTALL=1) ==="
fi

# Verify cluster is accessible
echo ""
echo "=== Verifying cluster ==="
ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" "kubectl version --client"
ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" "kubectl get nodes" || {
    echo "ERROR: Cannot access Kubernetes cluster"
    exit 1
}

# Show current EKCO deployment
echo ""
echo "=== Current EKCO deployment ==="
ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" "kubectl get deployment ekc-operator -n kurl -o wide" || true

# Patch EKCO deployment with test image
echo ""
echo "=== Patching EKCO deployment ==="
ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
    "kubectl set image deployment/ekc-operator -n kurl ekc-operator=$EKCO_IMAGE"

ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
    "kubectl set env deployment/ekc-operator -n kurl IMAGE_PULL_POLICY=Always"

ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
    "kubectl rollout restart deployment/ekc-operator -n kurl"

# Wait for rollout
echo ""
echo "=== Waiting for EKCO rollout ==="
if ! timeout 660 ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
    "kubectl rollout status deployment/ekc-operator -n kurl --timeout=600s"; then
    echo "WARNING: EKCO rollout status timed out; checking pod state..."
    ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
        "kubectl get pods -n kurl -l app=ekc-operator -o wide" || true
    ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
        "kubectl describe pods -n kurl -l app=ekc-operator" || true
    ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
        "kubectl logs -n kurl -l app=ekc-operator --all-containers=true --tail=100" || true
    exit 1
fi

# Health checks
echo ""
echo "=== Running health checks ==="

# Check pod is running
timeout 180 ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
    "kubectl wait --for=condition=ready pod -l app=ekc-operator -n kurl --timeout=120s" || {
    echo "ERROR: EKCO pod did not become ready"
    exit 1
}

# Get and save logs
LOG_FILE="$LOG_DIR/e2e-ekco-${TEST_TAG}.log"
echo "Saving EKCO logs to $LOG_FILE"
ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
    "kubectl logs -n kurl deployment/ekc-operator --tail=200" > "$LOG_FILE" 2>&1 || true

echo ""
echo "EKCO logs (last 50 lines):"
tail -50 "$LOG_FILE" 2>/dev/null || true

# Check for panic or fatal errors in logs
if grep -i "panic\|fatal error" "$LOG_FILE" 2>/dev/null; then
    echo "ERROR: Found panic/fatal errors in EKCO logs"
    exit 1
fi

# Check all pods in kurl namespace are running
ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
    "kubectl get pods -n kurl" || true

# Check EKCO deployment is available
ssh "${SSH_OPTS[@]}" -p "$VM_SSH_PORT" "$SSH_TARGET" \
    "kubectl get deployment ekc-operator -n kurl" || {
    echo "ERROR: Cannot get EKCO deployment status"
    exit 1
}

echo ""
echo "=== All health checks passed ==="
