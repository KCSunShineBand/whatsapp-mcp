#!/usr/bin/env bash
# bootstrap.sh — runs on first boot of the WhatsApp bridge VM.
# Idempotent: safe to re-run on restart.
set -euo pipefail

LOG="/var/log/bridge-bootstrap.log"
exec > >(tee -a "$LOG") 2>&1
echo "[$(date -u +%FT%TZ)] bootstrap start"

PROJECT_ID=$(curl -sH "Metadata-Flavor: Google" \
  http://metadata.google.internal/computeMetadata/v1/project/project-id)
echo "project=$PROJECT_ID"

# --- 1. System packages ---
export DEBIAN_FRONTEND=noninteractive
apt-get update -y
apt-get install -y ca-certificates curl gnupg git jq

# --- 2. Docker ---
if ! command -v docker >/dev/null; then
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg | \
    gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg
  ARCH=$(dpkg --print-architecture)
  CODENAME=$(. /etc/os-release && echo "$VERSION_CODENAME")
  echo "deb [arch=$ARCH signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $CODENAME stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -y
  apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
  systemctl enable --now docker
fi

# --- 3. Clone (or update) the forked bridge repo ---
# TEMPORARY: clone the feature branch until the hardening PR is merged to main.
# After PR merge, change BRANCH=main and re-run this script (or just `git pull` on the VM).
REPO_DIR="/opt/bridge/src"
REPO_URL="https://github.com/KCSunShineBand/whatsapp-mcp.git"
BRANCH="feat/leonglobal-hardening"
mkdir -p /opt/bridge/data

if [ ! -d "$REPO_DIR/.git" ]; then
  git clone --depth 1 --branch "$BRANCH" "$REPO_URL" "$REPO_DIR"
else
  git -C "$REPO_DIR" fetch --depth 1 origin "$BRANCH"
  git -C "$REPO_DIR" checkout "$BRANCH" 2>/dev/null || git -C "$REPO_DIR" checkout -B "$BRANCH" "origin/$BRANCH"
  git -C "$REPO_DIR" reset --hard "origin/$BRANCH"
fi

# --- 4. Fetch secrets from Secret Manager ---
BRIDGE_API_TOKEN=$(gcloud secrets versions access latest \
  --secret=whatsapp-bridge-api-token --project="$PROJECT_ID")
WEBHOOK_SECRET=$(gcloud secrets versions access latest \
  --secret=whatsapp-webhook-secret --project="$PROJECT_ID")

cat > /opt/bridge/.env <<EOF
BRIDGE_API_TOKEN=$BRIDGE_API_TOKEN
WEBHOOK_SECRET=$WEBHOOK_SECRET
WEBHOOK_URL=https://tickets.leon-global.com/api/webhooks/whatsapp
FORWARD_SELF=false
WHATSAPP_BRIDGE_PORT=8080
EOF
chmod 600 /opt/bridge/.env

# --- 5. Caddyfile ---
cat > /opt/bridge/Caddyfile <<'EOF'
bridge.leon-global.com {
  encode gzip
  reverse_proxy 127.0.0.1:8080 {
    header_up Host {host}
  }
  log {
    output stdout
    format json
  }
}
EOF

# --- 6. docker-compose.yml ---
cat > /opt/bridge/docker-compose.yml <<'EOF'
services:
  bridge:
    build:
      context: ./src/whatsapp-bridge
    restart: unless-stopped
    network_mode: host
    volumes:
      - /opt/bridge/data:/data
    env_file: /opt/bridge/.env
    working_dir: /data
    logging:
      driver: gcplogs

  caddy:
    image: caddy:2.8-alpine
    restart: unless-stopped
    network_mode: host
    volumes:
      - /opt/bridge/Caddyfile:/etc/caddy/Caddyfile
      - caddy_data:/data
      - caddy_config:/config
    logging:
      driver: gcplogs

volumes:
  caddy_data:
  caddy_config:
EOF

# --- 7. Start ---
cd /opt/bridge
docker compose pull || true
docker compose build
docker compose up -d

echo "[$(date -u +%FT%TZ)] bootstrap complete"
echo "NEXT STEPS (manual):"
echo "  1. Create DNS A record: bridge.leon-global.com -> <static IP>"
echo "  2. SSH in via IAP and scan QR:"
echo "     gcloud compute ssh whatsapp-bridge --zone=asia-southeast1-b --tunnel-through-iap"
echo "     cd /opt/bridge && docker compose logs -f bridge"
echo "  3. Verify: curl -H 'Authorization: Bearer <token>' https://bridge.leon-global.com/api/health"
