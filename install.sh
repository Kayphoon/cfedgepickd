#!/usr/bin/env sh
set -eu

repo="Kayphoon/cfpick"
version="latest"
mode="dry-run"
protocol="auto"
prefix="/usr/local/bin"
config="/etc/cfpick/config.json"
unit="/etc/systemd/system/cfpick.service"
start_service="false"
enable_service="true"

usage() {
  cat <<'USAGE'
Usage:
  install.sh [--dry-run|--apply] [--protocol auto|quic|http2] [options]

Options:
  --version VERSION   Release tag to install, for example v0.2.8. Default: latest
  --repo OWNER/REPO   GitHub repository. Default: Kayphoon/cfpick
  --prefix PATH       Binary install directory. Default: /usr/local/bin
  --config PATH       Config path. Default: /etc/cfpick/config.json
  --unit PATH         systemd unit path. Default: /etc/systemd/system/cfpick.service
  --start             Start/restart cfpick.service after installing
  --no-enable         Do not enable cfpick.service
  --help              Show this help

Examples:
  curl -fsSL https://raw.githubusercontent.com/Kayphoon/cfpick/main/install.sh | sh -s -- --dry-run
  curl -fsSL https://raw.githubusercontent.com/Kayphoon/cfpick/main/install.sh | sudo sh -s -- --apply --protocol auto
  curl -fsSL https://raw.githubusercontent.com/Kayphoon/cfpick/main/install.sh | sudo sh -s -- --apply --version v0.2.8 --start
USAGE
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --dry-run) mode="dry-run" ;;
    --apply) mode="apply" ;;
    --protocol) protocol="$2"; shift ;;
    --repo) repo="$2"; shift ;;
    --version) version="$2"; shift ;;
    --prefix) prefix="$2"; shift ;;
    --config) config="$2"; shift ;;
    --unit) unit="$2"; shift ;;
    --start) start_service="true" ;;
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
  case "$(uname -s)" in
    Linux) ;;
    *) echo "unsupported OS: $(uname -s); cfpick installer supports Linux only" >&2; exit 1 ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
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
  if [ ! -x "$package_dir/cfpick" ]; then
    echo "cfpick binary not found in $package_dir" >&2
    exit 1
  fi

  if [ "$mode" = "dry-run" ]; then
    "$package_dir/cfpick" install --protocol "$protocol" --config "$config" --binary "$prefix/cfpick" --unit "$unit"
    exit 0
  fi

  if [ "$(id -u)" != "0" ]; then
    echo "--apply writes to system paths; run as root or use sudo" >&2
    exit 1
  fi

  install -m 0755 "$package_dir/cfpick" "$prefix/cfpick"
  if [ -f "$package_dir/cfedgepickd" ]; then
    install -m 0755 "$package_dir/cfedgepickd" "$prefix/cfedgepickd"
  fi
  if [ -f "$package_dir/cfedgepickctl" ]; then
    install -m 0755 "$package_dir/cfedgepickctl" "$prefix/cfedgepickctl"
  fi

  "$prefix/cfpick" install --apply --protocol "$protocol" --config "$config" --binary "$prefix/cfpick" --unit "$unit"

  if command -v systemctl >/dev/null 2>&1; then
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
  fi

  echo "installed cfpick; inspect with: cfpick status"
  if [ "$start_service" != "true" ]; then
    echo "start with: systemctl start $(basename "$unit")"
  fi
}

script_dir="$(CDPATH= cd "$(dirname "$0")" && pwd)"
if [ -x "$script_dir/cfpick" ]; then
  install_from_dir "$script_dir"
fi

need_cmd curl
need_cmd tar
need_cmd mktemp
need_cmd uname

arch="$(detect_arch)"
asset="cfpick-linux-$arch.tar.gz"
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
