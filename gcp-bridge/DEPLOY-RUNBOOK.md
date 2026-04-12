# GCP WhatsApp Bridge Deployment Runbook

**Date:** 2026-04-12
**Operator:** KC
**VM:** whatsapp-bridge (asia-southeast1-b)
**Domain:** bridge.leon-global.com

---

## Prerequisites

- [ ] GCP project with billing enabled
- [ ] `gcloud` CLI authenticated with project owner/editor
- [ ] Static IP reserved (check with `gcloud compute addresses list`)
- [ ] Cloudflare access for leon-global.com DNS

---

## Step 1: Identify your GCP project and static IP

```bash
# Set your project (replace with actual project ID)
export GCP_PROJECT="your-gcp-project-id"
gcloud config set project $GCP_PROJECT

# Check existing static IP
gcloud compute addresses list --filter="region:asia-southeast1"
# Note the static IP address for DNS setup
```

---

## Step 2: Generate new secrets and store in Secret Manager

Generate strong random values for all secrets:

```bash
# Generate new secrets (copy these values — you'll also need them for Vercel)
BRIDGE_API_TOKEN=$(openssl rand -hex 32)
WEBHOOK_SECRET=$(openssl rand -hex 32)
CRON_SECRET=$(openssl rand -hex 32)

echo "BRIDGE_API_TOKEN=$BRIDGE_API_TOKEN"
echo "WEBHOOK_SECRET=$WEBHOOK_SECRET"
echo "CRON_SECRET=$CRON_SECRET"

# Save these somewhere secure temporarily — you'll need them for Vercel too
```

Create or update secrets in GCP Secret Manager:

```bash
# Create secrets (first time only — skip if they already exist)
echo -n "$BRIDGE_API_TOKEN" | gcloud secrets create whatsapp-bridge-api-token \
  --data-file=- --project=$GCP_PROJECT --replication-policy=user-managed \
  --locations=asia-southeast1

echo -n "$WEBHOOK_SECRET" | gcloud secrets create whatsapp-webhook-secret \
  --data-file=- --project=$GCP_PROJECT --replication-policy=user-managed \
  --locations=asia-southeast1

# OR update existing secrets (if they already exist)
echo -n "$BRIDGE_API_TOKEN" | gcloud secrets versions add whatsapp-bridge-api-token \
  --data-file=- --project=$GCP_PROJECT

echo -n "$WEBHOOK_SECRET" | gcloud secrets versions add whatsapp-webhook-secret \
  --data-file=- --project=$GCP_PROJECT
```

Verify the VM's service account can access the secrets:

```bash
# Get the VM's service account
SA=$(gcloud compute instances describe whatsapp-bridge \
  --zone=asia-southeast1-b --format='value(serviceAccounts[0].email)')
echo "Service Account: $SA"

# Grant access to both secrets
gcloud secrets add-iam-policy-binding whatsapp-bridge-api-token \
  --member="serviceAccount:$SA" --role="roles/secretmanager.secretAccessor" \
  --project=$GCP_PROJECT

gcloud secrets add-iam-policy-binding whatsapp-webhook-secret \
  --member="serviceAccount:$SA" --role="roles/secretmanager.secretAccessor" \
  --project=$GCP_PROJECT
```

---

## Step 3: Push hardened code to GitHub

Run these from your local machine:

```bash
# Push the Go bridge changes
cd "C:\Projects\LGPL team tasks\whatsapp-mcp"
git add -A
git commit -m "security: add bearer auth, path traversal fix, webhook timeout, bind localhost

- H1: Validate media_path with filepath.Clean + reject '..' segments
- H2: Add BRIDGE_API_TOKEN bearer auth middleware on /api/send,download,typing
- M8: Replace http.DefaultClient with 10s timeout client for webhooks
- M9: Log message length instead of content (privacy)
- L5: Bind HTTP server to 127.0.0.1 instead of 0.0.0.0
- L6: Document WEBHOOK_SECRET and BRIDGE_API_TOKEN in .env.example"
git push origin feat/leonglobal-hardening

# Push the ticket tracker changes
cd "C:\Projects\LGPL team tasks\lgpl-ticket-tracker"
git add -A
git commit -m "security: comprehensive hardening — 30 findings fixed

CRITICAL:
- Fix open redirect via ?next= parameter in auth callback
- Fix admin privilege escalation (app_metadata instead of user_metadata)

HIGH:
- Add HTTP security headers (CSP, HSTS, X-Frame-Options, etc.)
- Add OAuth CSRF nonce protection for Google Calendar + TickTick
- Sanitize all raw DB error messages returned to clients
- ICS token comparison now uses timingSafeEqual
- ALLOW_SELF_TICKETS forcibly disabled in production
- HMAC signature requires strict sha256= prefix

MEDIUM:
- Budget guard fails closed on DB errors
- Injection sentinel detection now case-insensitive
- findUserByName/findClientByName use DB-level ilike (not full table dump)
- updateAssigneesAction verifies ticket access via RLS
- OAuth callback routes added to PUBLIC_API_PREFIXES

LOW:
- HTML escaping in email templates
- Email format validation on user invite
- logUsage failure counter for monitoring"
git push origin master
```

---

## Step 4: Cloudflare DNS

In Cloudflare dashboard for leon-global.com:

| Type | Name | Content | Proxy | TTL |
|------|------|---------|-------|-----|
| A | bridge | `<STATIC_IP>` | DNS only (grey cloud) | Auto |

**Important:** Use "DNS only" (grey cloud), NOT "Proxied" (orange cloud).
Caddy on the VM handles TLS via Let's Encrypt. Cloudflare proxying would
break the ACME challenge.

---

## Step 5: Deploy the bridge VM

SSH into the VM and run the bootstrap:

```bash
# SSH via IAP
gcloud compute ssh whatsapp-bridge --zone=asia-southeast1-b --tunnel-through-iap

# On the VM: run bootstrap
sudo bash /opt/bridge/src/gcp-bridge/bootstrap.sh

# If bootstrap.sh isn't on the VM yet, pull the latest:
sudo mkdir -p /opt/bridge/src
sudo git clone --depth 1 --branch feat/leonglobal-hardening \
  https://github.com/KCSunShineBand/whatsapp-mcp.git /opt/bridge/src
sudo bash /opt/bridge/src/gcp-bridge/bootstrap.sh
```

---

## Step 6: QR code pairing

This is interactive — you need to scan a QR code with your WhatsApp mobile app:

```bash
# Still SSH'd into the VM:
cd /opt/bridge
sudo docker compose logs -f bridge
```

The bridge will print a QR code in the terminal. Scan it with:
1. Open WhatsApp on your phone
2. Go to Settings > Linked Devices > Link a Device
3. Scan the QR code displayed in the terminal

Wait for "Successfully logged in" in the logs, then Ctrl+C.

---

## Step 7: Verify bridge health

```bash
# From your local machine (or the VM):
curl -s -H "Authorization: Bearer $BRIDGE_API_TOKEN" \
  https://bridge.leon-global.com/api/health

# Expected: {"status":"ok"} or similar
```

---

## Step 8: Rotate secrets in Vercel

In the Vercel dashboard (https://vercel.com) for lgpl-ticket-tracker:

**Update these existing env vars:**

| Variable | New Value | Notes |
|----------|-----------|-------|
| `WHATSAPP_WEBHOOK_SECRET` | `$WEBHOOK_SECRET` (from Step 2) | Must match GCP secret |
| `CRON_SECRET` | `$CRON_SECRET` (from Step 2) | New rotated value |
| `WHATSAPP_BRIDGE_TOKEN` | `$BRIDGE_API_TOKEN` (from Step 2) | Must match GCP secret |

**Add this new env var:**

| Variable | Value | Notes |
|----------|-------|-------|
| `WHATSAPP_BRIDGE_URL` | `https://bridge.leon-global.com` | Now unblocks NL ticket intake |

**Also rotate these (generate new values in respective dashboards):**

| Variable | Where to rotate |
|----------|----------------|
| `SUPABASE_SERVICE_ROLE_KEY` | Supabase dashboard > Settings > API |
| `GOOGLE_CLIENT_SECRET` | Google Cloud Console > Credentials |
| `SMTP_PASS` | cPanel email account settings |
| `ANTHROPIC_API_KEY` | console.anthropic.com > API Keys |
| `TICKTICK_CLIENT_SECRET` | TickTick developer portal |

After updating all env vars, trigger a redeploy:

```bash
cd "C:\Projects\LGPL team tasks\lgpl-ticket-tracker"
npx vercel --prod
```

---

## Step 9: End-to-end verification

1. **Bridge health:**
   ```bash
   curl -H "Authorization: Bearer <token>" https://bridge.leon-global.com/api/health
   ```

2. **Webhook delivery:** Send a message to the LG Tickets WhatsApp group:
   > "Test ticket: verify bridge deployment works"

   Check Vercel function logs for the webhook hit.

3. **NL ticket creation:** Send a real ticket message:
   > "Assign Jai to VAPT revalidation for ACME Corp by next Friday"

   Verify ticket created in https://tickets.leon-global.com/board

4. **Daily digest:** Wait for 08:30 SGT or manually trigger:
   ```bash
   curl -X POST https://tickets.leon-global.com/api/cron/daily-digest \
     -H "Authorization: Bearer <CRON_SECRET>"
   ```

---

## Step 10: Remove ALLOW_SELF_TICKETS from Vercel

In Vercel dashboard, **delete** the `ALLOW_SELF_TICKETS` environment variable
entirely. The code now forcibly disables it in production regardless, but
removing the env var eliminates any confusion.

---

## Rollback

If the bridge causes issues:

```bash
# SSH into VM
gcloud compute ssh whatsapp-bridge --zone=asia-southeast1-b --tunnel-through-iap

# Stop the bridge
cd /opt/bridge && sudo docker compose down

# The ticket tracker will continue working — NL ticket creation returns
# errors but the board, dashboard, and manual ticket creation are unaffected.
```

---

## Post-deployment checklist

- [ ] Bridge health returns 200
- [ ] QR code paired successfully
- [ ] DNS resolves: `dig bridge.leon-global.com`
- [ ] TLS working: `curl -I https://bridge.leon-global.com/api/health`
- [ ] Webhook delivered to Vercel (check function logs)
- [ ] Test ticket created via WhatsApp
- [ ] All Vercel secrets rotated
- [ ] `ALLOW_SELF_TICKETS` removed from Vercel
- [ ] Daily digest fires at 08:30 SGT
