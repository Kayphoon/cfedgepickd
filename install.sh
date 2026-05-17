#!/usr/bin/env sh
set -eu

repo="Kayphoon/TunnelFlux"
version="latest"
mode="apply"
protocol="auto"
emergency_rtt_ms="0"
prefix="/usr/local/bin"
config="/etc/tunnelflux/config.json"
unit="/etc/systemd/system/tunnelflux.service"
start_service="true"
enable_service="true"

usage() {
  cat <<'USAGE'
Usage:
  install.sh [options]

Default behavior:
  Install the latest release, write the config, enable the service, and start it.

Options:
  --version VERSION   Release tag to install, for example v0.2.13. Default: latest
  --emergency-rtt-ms MS
                     Immediate hot-switch threshold in ms. 0 disables. Default: 0
  --no-start          Install the service but do not start it
  --help              Show this help

Advanced options:
  --dry-run           Preview discovery and planned writes without changing files
  --protocol MODE     auto, quic, or http2. Default: auto
  --repo OWNER/REPO   GitHub repository. Default: Kayphoon/TunnelFlux
  --prefix PATH       Binary install directory. Default: /usr/local/bin
  --config PATH       Config path. Default: /etc/tunnelflux/config.json
  --unit PATH         systemd unit or launchd plist path. Default: platform-specific
  --no-enable         Do not enable tunnelflux.service

Examples:
  curl -fsSL https://raw.githubusercontent.com/Kayphoon/TunnelFlux/main/install.sh | sudo sh
  curl -fsSL https://raw.githubusercontent.com/Kayphoon/TunnelFlux/main/install.sh | sudo sh -s -- --emergency-rtt-ms 100
  curl -fsSL https://raw.githubusercontent.com/Kayphoon/TunnelFlux/main/install.sh | sudo sh -s -- --version v0.2.13
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --dry-run) mode="dry-run" ;;
    --apply) mode="apply" ;;
    --protocol) protocol="$2"; shift ;;
    --emergency-rtt-ms) emergency_rtt_ms="$2"; shift ;;
    --repo) repo="$2"; shift ;;
    --version) version="$2"; shift ;;
    --prefix) prefix="$2"; shift ;;
    --config) config="$2"; shift ;;
    --unit) unit="$2"; shift ;;
    --start) start_service="true" ;;
    --no-start) start_service="false" ;;
    --no-enable) enable_service="false" ;;
    --help|-h) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

detect_arch() {
  os_name="linux"
  case "$(uname -s)" in
    Linux) os_name="linux" ;;
    Darwin)
      os_name="darwin"
      if [ "$unit" = "/etc/systemd/system/tunnelflux.service" ]; then
        unit="/Library/LaunchDaemons/com.kayphoon.tunnelflux.plist"
      fi
      ;;
    *) echo "unsupported OS: $(uname -s); TunnelFlux installer supports Linux and macOS" >&2; exit 1 ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) echo "$os_name-amd64" ;;
    aarch64|arm64) echo "$os_name-arm64" ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
}

release_base_url() {
  if [ "$version" = "latest" ]; then
    echo "https://github.com/$repo/releases/latest/download"
  else
    echo "https://github.com/$repo/releases/download/$version"
  fi
}

verify_checksum() {
  archive="$1"
  checksums="$2"
  asset="$3"
  [ -s "$checksums" ] || return 0
  grep "  $asset\$" "$checksums" > "$checksums.one" || return 0
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$(dirname "$archive")" && sha256sum -c "$(basename "$checksums.one")")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$(dirname "$archive")" && shasum -a 256 -c "$(basename "$checksums.one")")
  else
    echo "checksum file downloaded, but sha256sum/shasum is not available; skipping verification" >&2
  fi
}

install_from_dir() {
  package_dir="$1"
  if [ ! -x "$package_dir/tf" ]; then
    echo "tf binary not found in $package_dir" >&2
    exit 1
  fi

  if [ "$mode" = "dry-run" ]; then
    "$package_dir/tf" install --protocol "$protocol" --emergency-rtt-ms "$emergency_rtt_ms" --config "$config" --binary "$prefix/tf" --unit "$unit"
    exit 0
  fi

  if [ "$(id -u)" != "0" ]; then
    echo "install writes to system paths; run as root or use sudo" >&2
    exit 1
  fi

  install -m 0755 "$package_dir/tf" "$prefix/tf"

  "$prefix/tf" install --apply --protocol "$protocol" --emergency-rtt-ms "$emergency_rtt_ms" --config "$config" --binary "$prefix/tf" --unit "$unit"

  if [ "$(uname -s)" = "Linux" ] && command -v systemctl >/dev/null 2>&1; then
    unit_name="$(basename "$unit")"
    if [ "$enable_service" = "true" ]; then
      systemctl enable "$unit_name"
    fi
    if [ "$start_service" = "true" ]; then
      if systemctl is-active "$unit_name" >/dev/null 2>&1; then
        systemctl restart "$unit_name"
      else
        systemctl start "$unit_name"
      fi
    fi
  elif [ "$(uname -s)" = "Darwin" ] && command -v launchctl >/dev/null 2>&1; then
    label="$(basename "$unit" .plist)"
    if [ "$enable_service" = "true" ]; then
      launchctl bootstrap system "$unit" 2>/dev/null || true
    fi
    if [ "$start_service" = "true" ]; then
      launchctl kickstart -k "system/$label"
    fi
  fi

  echo "installed TunnelFlux; inspect with: tf status"
  if [ "$start_service" != "true" ]; then
    if [ "$(uname -s)" = "Darwin" ]; then
      echo "start with: launchctl kickstart -k system/$(basename "$unit" .plist)"
    else
      echo "start with: systemctl start $(basename "$unit")"
    fi
  fi
}

script_dir="$(CDPATH= cd "$(dirname "$0")" && pwd)"
if [ -x "$script_dir/tf" ]; then
  install_from_dir "$script_dir"
fi

need_cmd curl
need_cmd tar
need_cmd mktemp
need_cmd uname

platform="$(detect_arch)"
asset="tunnelflux-$platform.tar.gz"
base_url="$(release_base_url)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT INT TERM

echo "downloading $repo $version $asset"
curl -fsSL "$base_url/$asset" -o "$tmp_dir/$asset"
curl -fsSL "$base_url/checksums.txt" -o "$tmp_dir/checksums.txt" 2>/dev/null || true
verify_checksum "$tmp_dir/$asset" "$tmp_dir/checksums.txt" "$asset"

mkdir -p "$tmp_dir/package"
tar -xzf "$tmp_dir/$asset" -C "$tmp_dir/package"
install_from_dir "$tmp_dir/package"
