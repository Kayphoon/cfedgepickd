#!/usr/bin/env sh
set -eu

mode="dry-run"
protocol="auto"
prefix="/usr/local/bin"
config="/etc/cfpick/config.json"
unit="/etc/systemd/system/cfpick.service"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --dry-run) mode="dry-run" ;;
    --apply) mode="apply" ;;
    --protocol) protocol="$2"; shift ;;
    --prefix) prefix="$2"; shift ;;
    --config) config="$2"; shift ;;
    --unit) unit="$2"; shift ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

if [ "$mode" = "dry-run" ]; then
  ./cfpick install --protocol "$protocol" --config "$config" --binary "$prefix/cfpick" --unit "$unit"
  exit 0
fi

install -m 0755 ./cfpick "$prefix/cfpick"
if [ -f ./cfedgepickd ]; then install -m 0755 ./cfedgepickd "$prefix/cfedgepickd"; fi
if [ -f ./cfedgepickctl ]; then install -m 0755 ./cfedgepickctl "$prefix/cfedgepickctl"; fi
"$prefix/cfpick" install --apply --protocol "$protocol" --config "$config" --binary "$prefix/cfpick" --unit "$unit"
systemctl enable cfpick.service
echo "installed cfpick; inspect with: cfpick status; start with: systemctl start cfpick"
