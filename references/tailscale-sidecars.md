# Private access via Tailscale sidecars

Goal: reach each web UI privately from anywhere on your tailnet at a clean
`https://<service>.<tailnet>.ts.net` — with a real cert and **nothing on the
public internet** — without breaking the `arr` apps' container-to-container
calls.

## Two ways, pick based on need

- **Simplest (start here):** bind each UI port to the host's tailscale IP in
  compose (`100.x.x.x:9696:9696`, etc.). Reachable on the tailnet via
  `http://<host-tailscale-ip>:<port>`. No extra containers, no auth key. Good
  enough if you don't care about per-service hostnames.
- **Per-service sidecars (this file):** each service gets its own tailnet node
  and `https://<name>.<tailnet>.ts.net`. Nicer, but needs an auth key and care
  so the `arr` mesh keeps resolving each other by name.

## The mesh problem (why naïve sidecars break the arr stack)

`network_mode: service:<svc>-ts` puts a container in its sidecar's network
namespace — which **removes it from the `arr_default` bridge**. So Sonarr could
no longer reach `decypharr:8282` by name, breaking everything you wired.

**Fix:** keep the sidecar on the bridge with the **app's name as a network
alias**, and have the sidecar `serve` proxy inbound HTTPS to the local app port.
The app shares the sidecar's namespace, so:
- inside the tailnet: `https://sonarr.<tailnet>.ts.net` → sidecar :443 → `127.0.0.1:8989`
- between containers: `http://sonarr:8989` still resolves (alias on the bridge)

Use **userspace mode** (`TS_USERSPACE=true`) — no `/dev/net/tun`/NET_ADMIN needed —
together with `serve`. (Kernel mode can expose the real port directly but is
heavier and you'd have to rewrite the inter-app URLs to https; the
userspace+serve+alias combo keeps existing wiring untouched, which is why it wins.)

## Prerequisite: a reusable auth key

Generate at `https://login.tailscale.com/admin/settings/keys` → **Reusable: ON**
(you're registering several nodes), **Ephemeral: OFF** (nodes keep identity via
their state volume — ephemeral would churn them), optional tag. Copy the
`tskey-auth-...` secret (shown once). Existing one-time keys won't register new
nodes — test before assuming a key is reusable.

## serve.json (per service)

```json
{
  "TCP": { "443": { "HTTPS": true } },
  "Web": {
    "${TS_CERT_DOMAIN}:443": {
      "Handlers": { "/": { "Proxy": "http://127.0.0.1:8989" } }
    }
  }
}
```
Replace `8989` with the service's port (prowlarr 9696, radarr 7878, bazarr 6767,
jellyseerr 5055, decypharr 8282). Put each in `./<svc>-ts/serve.json`.

## Compose pattern (per service, e.g. sonarr)

```yaml
  sonarr-ts:
    image: tailscale/tailscale:latest
    container_name: sonarr-ts
    restart: unless-stopped
    hostname: sonarr
    environment:
      - TS_HOSTNAME=sonarr
      - TS_AUTHKEY=${TS_AUTHKEY:-}
      - TS_STATE_DIR=/var/lib/tailscale
      - TS_USERSPACE=true
      - TS_SERVE_CONFIG=/config/serve.json
    networks:
      default:
        aliases: [ sonarr ]      # ← keeps `http://sonarr:8989` working for siblings
    volumes:
      - tailscale-sonarr:/var/lib/tailscale
      - ./sonarr-ts:/config

  sonarr:
    # ... existing definition, but:
    network_mode: "service:sonarr-ts"
    # and REMOVE the `ports:` line (the sidecar now fronts it)
```
Add a matching `tailscale-<svc>` named volume for each. Repeat for every UI you
want a hostname for. Validate (`docker compose -p arr -f docker-compose.arr.yml config`), then
`up -d`. Check `tailscale status` shows the new nodes; confirm
`https://<svc>.<tailnet>.ts.net` returns the app, and that inter-app wiring
(Prowlarr↔Sonarr/Radarr, Sonarr/Radarr→Decypharr) still works.

Enable MagicDNS and HTTPS certificates in the Tailscale admin console. Use the
full `https://sonarr.<tailnet>.ts.net` name: the HTTPS certificate does not cover
the short `https://sonarr` name. Never enable Funnel for these private services.
Mount the config directory (not just the JSON file) so updates can be detected.
