# syntax=docker/dockerfile:1
# Caddy with Cloudflare DNS provider plugin for Let's Encrypt DNS-01 wildcard certificates.

FROM caddy:2-builder-alpine AS builder

RUN xcaddy build \
    --with github.com/caddy-dns/cloudflare

FROM caddy:2-alpine

COPY --from=builder /usr/bin/caddy /usr/bin/caddy
