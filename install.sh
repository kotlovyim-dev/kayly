#!/bin/sh
# Installs Kayly on a fresh Ubuntu or Debian server:
#   curl -fsSL https://raw.githubusercontent.com/kotlovyim-dev/kayly/main/install.sh | sudo sh
#
# Environment (all optional):
#   KAYLY_VERSION   release to install (default: latest)
#   KAYLY_BINARY    path to a kayly binary to install instead of downloading one (not checked)
#   KAYLY_BASE_DOMAIN  services get <service>.<project>.<base> (default: <ip>.sslip.io;
#                   "localhost" serves everything with Caddy's local certificates, for tests)
#   KAYLY_DOMAIN    hostname for the Kayly API (default: kayly.<base domain>)
#   KAYLY_EMAIL     contact email for Let's Encrypt
set -eu

REPO=kotlovyim-dev/kayly
# Pinned like everything Kayly runs, hashes included. Bump together with cmd/kayly/serve.go.
RAILPACK_VERSION=0.40.1

# Releases are signed: checksums.txt.sig is an ed25519 signature of checksums.txt by this key,
# the same one internal/upgrade/release.pub holds for `kayly upgrade`.
RELEASE_KEY='-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAp3Z5Wl4WjjWbQMePFkM5B6KqUq9f3zgCf3PK1tZ4VKQ=
-----END PUBLIC KEY-----'

say() { printf '\033[1m==> %s\033[0m\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "run as root (sudo sh)"
command -v curl >/dev/null || die "curl is required"
# Projects that follow a git repository are cloned over SSH by the daemon.
if ! command -v git >/dev/null || ! command -v ssh >/dev/null; then
  say "Installing git and OpenSSH client"
  apt-get update -qq && apt-get install -y -qq git openssh-client
fi
case "$(uname -m)" in
  x86_64) ARCH=amd64; RP_ARCH=x86_64; RP_SHA256=2842de93e68713af9037e0bc0a398d7da78f3b96aa4804303a638db2bc69bd30 ;;
  aarch64 | arm64) ARCH=arm64; RP_ARCH=arm64; RP_SHA256=c24a064b586b8f4f8c2fab44dd5ef19253e4c6cc4e1df793b3ae19cd87f7a5d4 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

if ! command -v docker >/dev/null; then
  say "Installing Docker"
  curl -fsSL https://get.docker.com | sh
fi
docker buildx version >/dev/null 2>&1 || die "docker buildx is missing; install the docker-buildx-plugin package"

# Railpack builds use a BuildKit feature Docker allows only with the containerd image store,
# the default on fresh installs since Docker 29. Older installs keep the classic store.
if ! docker info --format '{{json .DriverStatus}}' | grep -q io.containerd.snapshotter; then
  if [ -n "$(docker ps -aq)" ] || [ -s /etc/docker/daemon.json ]; then
    die "Docker uses the classic image store, which Railpack builds cannot use. Add \"features\": {\"containerd-snapshotter\": true} to /etc/docker/daemon.json and restart Docker (images in the old store become invisible), then run this again."
  fi
  say "Switching Docker to the containerd image store"
  printf '{ "features": { "containerd-snapshotter": true } }\n' >/etc/docker/daemon.json
  systemctl restart docker
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# fetch URL FILE SHA256: download and verify, or stop.
fetch() {
  curl -fsSL "$1" -o "$2"
  echo "$3  $2" | sha256sum -c --quiet - || die "checksum mismatch for $1"
}

say "Installing Railpack $RAILPACK_VERSION"
rp="railpack-v$RAILPACK_VERSION-$RP_ARCH-unknown-linux-musl.tar.gz"
fetch "https://github.com/railwayapp/railpack/releases/download/v$RAILPACK_VERSION/$rp" "$TMP/$rp" "$RP_SHA256"
tar -xzf "$TMP/$rp" -C "$TMP" railpack
install -m 755 "$TMP/railpack" /usr/local/bin/railpack

if [ -n "${KAYLY_BINARY:-}" ]; then
  say "Installing kayly from $KAYLY_BINARY"
  install -m 755 "$KAYLY_BINARY" /usr/local/bin/kayly
else
  version=${KAYLY_VERSION:-$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')}
  [ -n "$version" ] || die "could not find the latest Kayly release"
  say "Installing kayly $version"
  url="https://github.com/$REPO/releases/download/$version"
  command -v openssl >/dev/null || { apt-get update -qq && apt-get install -y -qq openssl; }
  curl -fsSL "$url/checksums.txt" -o "$TMP/checksums.txt"
  curl -fsSL "$url/checksums.txt.sig" -o "$TMP/checksums.txt.sig" || die "release $version is not signed"
  printf '%s\n' "$RELEASE_KEY" >"$TMP/release.pub"
  # -rawin (OpenSSL 3, in Ubuntu 22.04+ and Debian 12+) verifies a plain ed25519 signature.
  openssl pkeyutl -verify -pubin -inkey "$TMP/release.pub" -rawin -in "$TMP/checksums.txt" -sigfile "$TMP/checksums.txt.sig" >/dev/null 2>&1 ||
    die "the signature of release $version does not verify; not installing it"
  sum=$(awk -v f="kayly-linux-$ARCH" '$2 == f { print $1 }' "$TMP/checksums.txt")
  [ -n "$sum" ] || die "no checksum for kayly-linux-$ARCH"
  fetch "$url/kayly-linux-$ARCH" "$TMP/kayly" "$sum"
  install -m 755 "$TMP/kayly" /usr/local/bin/kayly
fi

if [ -n "${KAYLY_BASE_DOMAIN:-}" ]; then
  base=$KAYLY_BASE_DOMAIN
else
  ip=$(curl -fsS4 https://api.ipify.org) || die "could not detect the public IPv4 address"
  base="$(echo "$ip" | tr . -).sslip.io"
fi
domain=${KAYLY_DOMAIN:-kayly.$base}

say "Starting the kayly service"
cat >/etc/systemd/system/kayly.service <<EOF
[Unit]
Description=Kayly deploy daemon
After=docker.service network-online.target
Requires=docker.service
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/kayly serve --base-domain $base --domain $domain --railpack /usr/local/bin/railpack ${KAYLY_EMAIL:+--email $KAYLY_EMAIL}
RuntimeDirectory=kayly
RuntimeDirectoryPreserve=yes
StateDirectory=kayly
StateDirectoryMode=0700
Restart=always
RestartSec=2
# Running deploys finish before the daemon stops (it waits up to 9 minutes).
TimeoutStopSec=600
KillMode=mixed

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now kayly >/dev/null 2>&1
systemctl restart kayly

i=0
until [ -S /run/kayly/admin.sock ]; do
  i=$((i + 1))
  [ $i -lt 120 ] || die "kayly did not start; see: journalctl -u kayly"
  sleep 1
done

token=$(kayly token create "$(hostname)-install" | head -n 1)
cat <<EOF

Kayly is running.

  API:            https://$domain
  Your apps get:  https://<service>.<project>.$base

On your laptop:
  kayly login https://$domain $token
  kayly deploy

Logs: journalctl -u kayly -f
EOF
