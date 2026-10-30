#!/bin/bash

# fallback script, called in case the configured 'Startup Master Template' fails
# the script uses the CLI client to do basic configuration and make P4wnP1 reachable
# Additionally it serves as example on how to use the CLI client to configure P4wnP1 A.L.O.A.

# Bring wlan0 up as the P4wnP1 AP first so the board is reachable over WiFi
# even when USB gadget init is still failing.
P4wnP1_cli wifi set ap -r US -c 6 -s "P4wnP1" -k "MaMe82-P4wnP1" --nonexmon
P4wnP1_cli net set server -i wlan0 -a 172.24.0.1 -m 255.255.255.0 -o "3:" -o "6:" -r "172.24.0.10|172.24.0.20|5m"

# Enable USB ethernet (RNDIS+CDC ECM), HID keyboard/mouse for HIDScript, and mass storage.
UMS_DIR=/usr/local/P4wnP1/ums/flashdrive
UMS_IMG="$UMS_DIR/default.bin"
mkdir -p "$UMS_DIR"
if [ ! -f "$UMS_IMG" ]; then
	dd if=/dev/zero of="$UMS_IMG" bs=1M count=32 status=none
	mkfs.vfat "$UMS_IMG" >/dev/null 2>&1 || true
fi
P4wnP1_cli usb set --vid 0x1d6b --pid 0x134B --manufacturer "MaMe82" --sn "deadbeef1337" --product "P4wnP1 by MaMe82" --rndis --hid-keyboard --hid-mouse --ums --ums-file default.bin

P4wnP1_cli net set server -i usbeth -a 172.16.0.1 -m 255.255.255.248 -o "3:" -o "6:" -r "172.16.0.2|172.16.0.2|5m"

P4wnP1_cli led -b 2
