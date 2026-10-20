# Domains and HTTPS

Every service with a `port` is served at `https://<service>.<project>.<ip>.sslip.io` from its first deploy, with a Let's Encrypt certificate. [sslip.io](https://sslip.io) resolves any name that contains an IP address to that address, so this works before you own a domain.

## Add your own

1. Point an A record (and AAAA, if the server has IPv6) for the hostname at the server.
2. Add it:

   ```sh
   kayly domains add app.example.com web
   ```

   or in the UI: the project's Settings, Domains.

The proxy (Caddy) requests a certificate when the first request arrives and renews it on its own. If the A record does not point at the server yet, issuance fails and is retried; fix the record and wait a minute.

Templates such as `${web.url}` in `kayly.toml` resolve to the first custom domain of the service, else its sslip.io address. They pick up a new domain on the next deploy or `kayly redeploy`.

## What the proxy does

- HTTP/1.1, HTTP/2 and HTTP/3; HTTP redirects to HTTPS.
- WebSocket and server-sent events pass through, flushed at once, even with compression on (gzip and zstd).
- No request body limit and no read timeout: uploads and long requests are the app's business.
- A switch to a new release is one atomic config load; open connections finish on the old containers.

## Ports the proxy cannot carry

UDP media, TURN or game servers publish their ports directly on the host:

```toml
[services.livekit]
ports = ["7882/udp", "3478/udp"]
```

Such a service stops before its successor starts (two containers cannot bind one port), so its deploys have a short gap.
