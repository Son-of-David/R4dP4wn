# Modifications to P4wnP1 A.L.O.A.

This document satisfies GNU GPL version 3 section 5(a): this work **is a
modified version** of P4wnP1 A.L.O.A., and the changes are identified here.

| | |
| --- | --- |
| Original program | P4wnP1 A.L.O.A. |
| Original copyright | Copyright (C) 2018 Marcus Mengs (MaMe82) |
| Original license | GNU GPL v3 or later (`LICENSE`) |
| This version | R4d-P4wn |
| Modifier | Son-of-David |
| Modification dates | 2026 |
| Compared against | https://github.com/RoganDawes/P4wnP1_aloa commit `a56bb4d` (2023-10-26) |

This modified version remains licensed under **GNU GPL v3 or later**. The
entire combined work is released under that license. See `LICENSE` and `NOTICE`.

The compiled web UI (`dist/www/webapp.js`) is generated from `web_client/`
and is not present in the upstream git tree (it was gitignored). It is
included here so the board image has a working UI.

---

## Summary of functional changes

These are the user-visible and platform changes relative to stock P4wnP1 A.L.O.A.:

- Rebranded UI title **R4d-P4wn**, with credit that the program is MaMe82's
  work **modified and updated by Son-of-David**.
- Ported the service to **arm64** (Radxa Zero 3E / RK3566, Armbian) while
  keeping USB gadget ethernet, HID, and UMS.
- WiFi: onboard **AP first**, USB adapter **wlan1** as client
  (`STA_FAILOVER_AP`), with stored client SSID/PSK.
- Internet **passthrough** (NAT) from wlan1 to USB ethernet (`usbeth`) and
  the AP, with optional boot autostart.
- **VPN** (WireGuard / OpenVPN) connect from the web UI, boot autostart
  after wlan1 is up, share-over-VPN after handshake, configurable DNS
  (default `8.8.8.8,1.1.1.1`, off, or custom).
- Extra web pages: **Kismet**, **GPS**, **Airgeddon**, **Terminal**,
  **WiFi client**, **Share/VPN**, **Capture** (tshark/tcpdump write-only pcap).
- USB gadget: RNDIS-oriented Windows reconnect / UDC rebind behavior.
- DHCP/DNS defaults for usbeth so Windows hosts get a gateway and DNS.

---

## Files added (not in upstream `a56bb4d`)

| Path | Role |
| --- | --- |
| `NOTICE` | GPL attribution for original vs modified work |
| `AUTHORS` | Author credits |
| `MODIFICATIONS.md` | This file |
| `service/share.go` | Internet share + VPN manager and `/api/share` |
| `service/capture.go` | Packet capture manager and `/api/capture` |
| `service/wifi_sta.go` | wlan1 client join, boot networking, STA helpers |
| `service/wifi_ap.go` | Access-point bring-up helpers |
| `service/kismet.go` | Kismet control API |
| `service/gps.go` | gpsd / GPS page API |
| `service/airgeddon.go` | Airgeddon control API |
| `service/webterm.go` | Web terminal subsystem |
| `service/ptysession.go` | PTY/WebSocket terminal sessions |
| `service/dhcp4_client.go` | Built-in DHCPv4 client fallback |
| `service/store_seed.go` | Datastore defaults seeding |
| `dist/www/share.js` | Share / VPN web page |
| `dist/www/capture.js` | Capture web page |
| `dist/www/kismet.js` | Kismet web page |
| `dist/www/wifi-sta.js` | WiFi client page extras |
| `dist/www/term.js` | Terminal web page |
| `dist/www/airgeddon.js` | Airgeddon web page |
| `dist/www/shell.js` | Shell helper page |
| `dist/www/webmap.js` | Map helper for GPS |
| `dist/www/license.js` | License / modification notices in the web UI |
| `dist/www/LICENSE.txt` | Copy of the GNU GPL served by the web UI |
| `dist/scripts/rename-usb-wifi.sh` | Rename USB WiFi adapter to `wlan1` |
| `build_support/preserve_native_map.py` | Build helper |

---

## Files modified versus upstream `a56bb4d`

Line counts are `diff -u` added/removed against that commit (approximate).

| Path | Change (approx.) | What changed |
| --- | --- | --- |
| `service/service.go` | +63 −11 | Register share/capture/kismet/gps/airgeddon/webterm; boot networking goroutine |
| `service/wifi.go` | +185 −83 | Dual-radio AP then wlan1 client; arm64/hostapd bring-up |
| `service/rpc_server.go` | +110 −57 | HTTP routes for extra APIs before gRPC-web |
| `service/dhcp.go` | +8 −1 | DNS forwarders in dnsmasq config |
| `service/defaults.go` | +28 −31 | usbeth DHCP gateway/DNS defaults |
| `service/SubSysUSB.go` | +480 −73 | USB gadget / RNDIS / UDC reconnect for this board |
| `service/SubSysNetworkManager.go` | +28 −1 | usbeth DHCP option normalization |
| `service/dwc2_connect_watcher.go` | +18 −11 | Re-apply internet share when a USB host connects |
| `service/SubSysTriggerAction.go` | | Wiring for extra subsystems |
| `service/Event.go` | | Event path updates |
| `service/bluetooth.go` | | Board/build compatibility |
| `service/bluetooth/controller.go` | | Board/build compatibility |
| `service/datastore/store.go` | | Store compatibility |
| `cmd/P4wnP1_service/P4wnP1_service.go` | | arm64 build tags; startup legal notice |
| `cli_client/cmd_usb_new.go` | | USB CLI adjustments |
| `cli_client/rpc_client.go` | | Client adjustments |
| `common_web/Globals.go` | | Version credit names the modification |
| `web_client/main.go` | +13 −3 | R4d-P4wn title, extra tabs, version credit |
| `web_client/hvueComponentWiFi.go` | +27 −2 | Client+AP / wlan1 UI |
| `web_client/hvueCompUSBSettings.go` | | Branding / USB labels |
| `web_client/hvueComponentsBluetooth.go` | | Branding labels |
| `web_client/mvuexGlobalState.go` | +5 −2 | Title R4d-P4wn |
| `web_client/globals.go` | | Example HID text |
| `web_client/jsDataHandling.go` | | Client data handling |
| `web_client/rpcClient.go` | | RPC client |
| `web_client/rbuild.sh` | | Web client build |
| `dist/www/index.html` | +10 −3 | Title, extra scripts, HTML copyright comment |
| `dist/www/webapp.js` | generated | Compiled UI matching `web_client/` |
| `dist/P4wnP1.service` | | systemd unit for this install |
| `dist/scripts/servicestart.sh` | | Startup script |
| `Makefile` | | Extra packages (kismet, gpsd, airgeddon, …) |
| `build_support/build.sh` | | arm64 Go build |
| `rbuild_debug.sh` | | arm64 debug build |
| `go.mod` / `go.sum` | | Module checksums for the toolchain used |

Unchanged original files remain MaMe82's work under the same GNU GPL.

---

## How to regenerate this file list

From a checkout of this tree:

```sh
git clone --depth 1 https://github.com/RoganDawes/P4wnP1_aloa.git /tmp/P4wnP1_aloa_upstream
diff -rq --exclude=vendor --exclude=.git --exclude=build \
  /tmp/P4wnP1_aloa_upstream .
```

---

## Interactive notice

The web UI footer and subtitle state that this is MaMe82's program modified
by Son-of-David, licensed under GNU GPL v3. The License tab (or
`/license`) repeats that notice and links to `LICENSE.txt` served from the
web root.
