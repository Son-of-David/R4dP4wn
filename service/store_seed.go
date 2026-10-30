//go:build linux && (arm || arm64)
// +build linux
// +build arm arm64

package service

import (
	"log"
	"os"
	"strings"

	"github.com/mame82/P4wnP1_aloa/common"
	pb "github.com/mame82/P4wnP1_aloa/proto"
	"github.com/mame82/P4wnP1_aloa/service/datastore"
)

func (svc *Service) seedStoreDefaults() {
	store := svc.SubSysDataStore
	if store == nil {
		return
	}

	putIfMissing := func(key string, val interface{}) {
		if store.Exists(key) {
			return
		}
		if err := store.Put(key, val, true); err != nil {
			log.Printf("datastore seed %s: %v", key, err)
		} else {
			log.Printf("datastore seeded %s", key)
		}
	}

	gs := GetDefaultGadgetSettings()
	putIfMissing(cSTORE_PREFIX_USB_SETTINGS+"default", &gs)
	putIfMissing(cSTORE_PREFIX_WIFI_SETTINGS+"default", GetDefaultWiFiSettings())
	putIfMissing(cSTORE_PREFIX_BLUETOOTH_SETTINGS+"default", GetDefaultBluetoothSettings())

	usbNet := GetDefaultNetworkSettingsUSB()
	wifiNet := GetDefaultNetworkSettingsWiFi()
	btNet := GetDefaultNetworkSettingsBluetooth()
	putIfMissing(cSTORE_PREFIX_ETHERNET_INTERFACE_SETTINGS+usbNet.Name+"_default", usbNet)
	putIfMissing(cSTORE_PREFIX_ETHERNET_INTERFACE_SETTINGS+wifiNet.Name+"_default", wifiNet)
	putIfMissing(cSTORE_PREFIX_ETHERNET_INTERFACE_SETTINGS+btNet.Name+"_default", btNet)

	putIfMissing(cSTORE_PREFIX_MASTER_TEMPLATE+"startup", &pb.MasterTemplate{
		TemplateNameUsb:       "default",
		TemplateNameWifi:      "default",
		TemplateNameBluetooth: "default",
		TemplateNamesNetwork: []string{
			usbNet.Name + "_default",
			wifiNet.Name + "_default",
		},
	})
	putIfMissing(cSTORE_STARTUP_MASTER_TEMPLATE, &pb.StringMessage{Msg: "startup"})
	refreshStoredUSBWindowsCompat(store)

	initPath := common.PATH_DATA_STORE_BACKUP + "/init.db"
	if _, err := os.Stat(initPath); os.IsNotExist(err) {
		if err := os.MkdirAll(common.PATH_DATA_STORE_BACKUP, 0755); err != nil {
			log.Printf("could not create %s: %v", common.PATH_DATA_STORE_BACKUP, err)
			return
		}
		if err := store.Backup(initPath); err != nil {
			log.Printf("could not write %s: %v", initPath, err)
		} else {
			log.Printf("wrote initial datastore backup to %s", initPath)
		}
	}
}

func refreshStoredUSBWindowsCompat(store *datastore.Store) {
	usbKeys, err := store.KeysPrefix(cSTORE_PREFIX_USB_SETTINGS, true)
	if err != nil {
		log.Printf("could not list USB templates: %v", err)
	}
	for _, name := range usbKeys {
		gs := &pb.GadgetSettings{}
		key := cSTORE_PREFIX_USB_SETTINGS + name
		if err := store.Get(key, gs); err != nil {
			continue
		}
		changed := false
		pid := strings.ToLower(strings.TrimSpace(gs.Pid))
		if pid == "0x1347" || pid == "0x1349" {
			gs.Pid = "0x134B"
			changed = true
		}
		if changed {
			if err := store.Put(key, gs, true); err != nil {
				log.Printf("could not update USB template %s: %v", name, err)
			} else {
				log.Printf("USB template %q: migrated PID %s -> 0x134B", name, pid)
			}
		}
	}

	netKeys, err := store.KeysPrefix(cSTORE_PREFIX_ETHERNET_INTERFACE_SETTINGS, true)
	if err != nil {
		log.Printf("could not list ethernet templates: %v", err)
		return
	}
	for _, name := range netKeys {
		eis := &pb.EthernetInterfaceSettings{}
		key := cSTORE_PREFIX_ETHERNET_INTERFACE_SETTINGS + name
		if err := store.Get(key, eis); err != nil {
			continue
		}
		if eis.Name != USB_ETHERNET_BRIDGE_NAME && !strings.Contains(name, "usbeth") {
			continue
		}
		if eis.DhcpServerSettings == nil {
			continue
		}
		if eis.DhcpServerSettings.Options == nil {
			eis.DhcpServerSettings.Options = map[uint32]string{}
		}
		gw := eis.IpAddress4
		if gw == "" {
			gw = "172.16.0.1"
		}
		changed := false
		if eis.DhcpServerSettings.Options[3] == "" {
			eis.DhcpServerSettings.Options[3] = gw
			changed = true
		}
		if eis.DhcpServerSettings.Options[6] == "" {
			eis.DhcpServerSettings.Options[6] = gw
			changed = true
		}
		if changed {
			if err := store.Put(key, eis, true); err != nil {
				log.Printf("could not update ethernet template %s: %v", name, err)
			} else {
				log.Printf("ethernet template %q: usbeth DHCP router/DNS set to %s", name, gw)
			}
		}
	}
}
