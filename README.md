# Zoraxy Cloudflare Tunnel plugin

<p align="center">
  <img src="public/logo.svg" alt="Zoraxy Cloudflare Tunnel logo" width="160">
</p>

A Zoraxy Utilities plugin that manages one remotely-managed Cloudflare Tunnel and maps public hostnames to Zoraxy.

Default origin:

```text
https://127.0.0.1:443
```

For HTTPS origins the plugin enables `matchSNItoHost` by default, so Zoraxy can select the certificate / virtual host using the incoming hostname. TLS verification is enabled by default.

## How it works

### Automatic configuration
```text
Plugin dashboard ──▶ Cloudflare API (tunnel, ingress and DNS configuration)
```

### Tunnel proxy
```text
Browser ──▶ Cloudflare edge ──▶ Cloudflare Tunnel
                                      │  (outbound tunnel connection)
                                      ▼
                               cloudflared (child process)
                                      │  (HTTPS + incoming Host/SNI)
                                      ▼
                                Zoraxy (TLS) ──▶ origin application
```

## Features

- Store Cloudflare Account ID, Zone ID and scoped API token.
- Create or reuse a remotely-managed Cloudflare Tunnel.
- Configure Cloudflare Tunnel ingress rules.
- Upsert proxied CNAME records pointing to `<tunnel-id>.cfargotunnel.com`.
- Start and stop `cloudflared` as a child process using the `TUNNEL_TOKEN` environment variable.
- Persistent configuration with file mode `0600`.
- Embedded UI, with live files from `./www` when Zoraxy reports a development build.
- Auto-start option: starts the tunnel when the plugin starts/restarts and immediately after **Apply to Cloudflare**.
- Live `cloudflared` availability status with runtime controls disabled when it is missing.

## Cloudflare token permissions

Create a scoped API token with:

- Account → Cloudflare Tunnel → Edit
- Zone → DNS → Edit

## Screenshot

<img width="1099" height="1142" alt="image" src="https://github.com/user-attachments/assets/0ab60cda-a3f6-4164-8dc0-d37828d9f17f" />

## Requirements

- Zoraxy with plugin support.
- Go 1.23+ to build from source.
- `cloudflared` installed on the same host/environment as the Zoraxy plugin process.

## Install cloudflared

The plugin does not bundle or install the Cloudflare daemon. Install it manually inside the **same host, LXC, or container as Zoraxy** by following [Cloudflare's official download and installation guide](https://developers.cloudflare.com/tunnel/downloads/), then verify it with `cloudflared --version`.

The package normally installs the executable as `/usr/bin/cloudflared`. The plugin also checks common locations such as `/usr/bin/cloudflared` and `/usr/local/bin/cloudflared` when the Zoraxy systemd service has a restricted `PATH`. You can always enter the absolute path in **cloudflared executable**.

Do not install a separate `cloudflared` systemd tunnel service when using the plugin's process manager; the plugin starts/stops the connector itself.

## Build

```bash
go test ./...
go build -o cloudflare-tunnel .
./cloudflare-tunnel -introspect
```

## Install for Zoraxy development

Assuming the Zoraxy working directory contains `plugins/`:

```bash
mkdir -p plugins/cloudflare-tunnel
cp /path/to/zoraxy-cloudflare-tunnel/cloudflare-tunnel plugins/cloudflare-tunnel/cloudflare-tunnel
chmod +x plugins/cloudflare-tunnel/cloudflare-tunnel
```

Restart your normal Zoraxy service after the first install, then open **Plugins** and enable **Cloudflare Tunnel**. If your Zoraxy build exposes **Plugin Auto Reload**, you can enable that developer option so replacing/rebuilding an already loaded plugin binary causes it to reload automatically.

A convenient development loop is to build directly into the plugin directory:

```bash
cd /path/to/zoraxy-cloudflare-tunnel
go build -o /path/to/zoraxy/plugins/cloudflare-tunnel/cloudflare-tunnel .
```

If Zoraxy reports `DevelopmentBuild` to the plugin, the plugin serves `./www` from disk when that directory exists. Backend Go changes still require rebuilding the binary.

## First configuration

1. Install `cloudflared` manually and verify it with `cloudflared --version`.
2. Open the plugin UI.
3. Enter Account ID, Zone ID and the API token.
4. Keep the origin as `https://127.0.0.1:443` unless your Zoraxy HTTPS listener is different.
5. Add public hostnames such as `app.example.com`.
6. Click **Apply to Cloudflare**. This creates/reuses the tunnel, updates ingress, and upserts DNS.
7. Either click **Start tunnel**, or enable **Auto-start tunnel when Zoraxy/plugin starts**. With auto-start enabled, **Apply to Cloudflare** also starts the tunnel immediately.
8. Make sure Zoraxy already has an HTTP proxy rule for each hostname.

## TLS notes

With the default:

```text
service = https://127.0.0.1:443
matchSNItoHost = true
```

`cloudflared` connects to Zoraxy over HTTPS and sets SNI to the incoming public hostname. Zoraxy therefore needs to present a certificate trusted by the environment running `cloudflared` for that hostname. Only enable **Disable origin TLS verification** for intentionally self-signed/untrusted origin certificates.

## Docker

The plugin runs inside the Zoraxy container if Zoraxy is containerized. Therefore `cloudflared` must also be available inside that container (or this process-management part must be adapted to control a separate cloudflared container). The default `127.0.0.1:443` refers to the Zoraxy container itself.

## Security

The API token is returned to the UI only as a `token_configured` boolean and is not included in status output. It is stored in `cloudflare-tunnel.json` with mode `0600`. Tunnel runtime tokens are fetched on start and passed to `cloudflared` using the `TUNNEL_TOKEN` environment variable rather than as a command-line argument.

## Current limitations

- One Cloudflare account, zone, and tunnel per plugin instance.
- Removing a hostname from the plugin removes it from tunnel ingress, but does not delete its existing Cloudflare DNS record. This deliberately avoids destructive DNS changes.
- The plugin does not currently import Zoraxy proxy rules automatically.
- The plugin detects `cloudflared` but does not install or update it.

This project is an independent community plugin and is not an official Cloudflare or Zoraxy product.
