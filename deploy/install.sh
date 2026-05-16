#!/usr/bin/env sh
set -eu

mode="dry-run"
protocol="auto"
prefix="/usr/local/bin"
config="/etc/cfedgepickd/config.json"
unit="/etc/systemd/system/cfedgepickd.service"

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
  ./cfedgepickctl install --protocol "$protocol" --config "$config" --binary "$prefix/cfedgepickd" --unit "$unit"
  exit 0
fi

install -m 0755 ./cfedgepickd "$prefix/cfedgepickd"
install -m 0755 ./cfedgepickctl "$prefix/cfedgepickctl"
"$prefix/cfedgepickctl" install --apply --protocol "$protocol" --config "$config" --binary "$prefix/cfedgepickd" --unit "$unit"
systemctl enable cfedgepickd.service
echo "installed cfedgepickd; start with: systemctl start cfedgepickd"

