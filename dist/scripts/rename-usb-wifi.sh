#!/usr/bin/env bash
set -euo pipefail
sleep 2
declare -A used
while read -r n; do
  idx="${n#wlan}"
  [[ "$idx" =~ ^[0-9]+$ ]] && used["$idx"]=1
done < <(ip -o link show | awk -F': ' '{print $2}' | sed 's/@.*//' | grep -E '^wlan[0-9]+$' || true)
used[0]=1  # never touch wlan0
next_free_wlan() {
  local i=1
  while [[ -n "${used[$i]:-}" ]]; do
    ((i++))
  done
  echo "$i"
}
for iface in /sys/class/net/*; do
  iface="${iface##*/}"
  # wireless only
  [[ -d "/sys/class/net/$iface/wireless" ]] || continue
  # keep onboard primary wifi unchanged
  [[ "$iface" == "wlan0" ]] && continue
  # if already wlanN, reserve and skip
  if [[ "$iface" =~ ^wlan[1-9][0-9]*$ ]]; then
    used["${iface#wlan}"]=1
    continue
  fi
  target_idx="$(next_free_wlan)"
  target="wlan${target_idx}"
  echo "Renaming $iface -> $target"
  ip link set dev "$iface" down || true
  iw dev "$iface" set name "$target" 2>/dev/null || ip link set dev "$iface" name "$target"
  ip link set dev "$target" up || true
  used["$target_idx"]=1
done
