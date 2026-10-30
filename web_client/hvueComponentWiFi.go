// +build js

package main

import (
	"github.com/gopherjs/gopherjs/js"
	pb "github.com/mame82/P4wnP1_aloa/proto/gopherjs"
	"github.com/mame82/hvue"
)

func InitComponentsWiFi() {
	hvue.NewComponent(
		"wifi",
		hvue.Template(templateWiFi),
		hvue.DataFunc(func(vm *hvue.VM) interface{} {
			data := struct {
				*js.Object
				ShowStoreModal bool   `js:"showStoreModal"`
				ShowLoadModal bool   `js:"showLoadModal"`
				ShowDeployStoredModal bool   `js:"showDeployStoredModal"`
				TemplateName   string `js:"templateName"`
			}{Object: O()}
			data.ShowStoreModal = false
			data.ShowLoadModal = false
			data.ShowDeployStoredModal = false
			data.TemplateName = ""
			return &data
		}),
		hvue.Computed("settings", func(vm *hvue.VM) interface{} {
			return vm.Get("$store").Get("state").Get("wifiState").Get("CurrentSettings")
		}),
		hvue.Computed("wifiState", func(vm *hvue.VM) interface{} {
			return vm.Get("$store").Get("state").Get("wifiState")
		}),
		hvue.Computed("wifiStateIcon", func(vm *hvue.VM) interface{} {
			mode := vm.Get("$store").Get("state").Get("wifiState").Get("mode").Int()
			switch mode {
			case int(pb.WiFiStateMode_STA_CONNECTED):
				// Client mode, connected
				return "wifi"
			case int(pb.WiFiStateMode_AP_UP):
				// Access Point running
				return "wifi_tethering"
			default:
				return "portable_wifi_off"
			}
		}),
		hvue.Computed("wifiStateText", func(vm *hvue.VM) interface{} {
			mode := vm.Get("$store").Get("state").Get("wifiState").Get("mode").Int()
			ssid := vm.Get("$store").Get("state").Get("wifiState").Get("ssid").String()
			channel := vm.Get("$store").Get("state").Get("wifiState").Get("channel").String()
			switch mode {
			case int(pb.WiFiStateMode_STA_CONNECTED):
				res := "Connected to network"
				if len(ssid) > 0 {
					res += ": '" + ssid + "' on channel " + channel
				}
				return res
			case int(pb.WiFiStateMode_AP_UP):
				res := "Access Point running on channel " + channel
				if len(ssid) > 0 {
					res += ": '" + ssid + "'"
				}
				return res
			default:
				return "Not connected"
			}
		}),
		hvue.ComputedWithGetSet("enabled",
			func(vm *hvue.VM) interface{} {
				return !vm.Get("settings").Get("disabled").Bool()
			},
			func(vm *hvue.VM, newValue *js.Object) {
				vm.Get("settings").Set("disabled", !newValue.Bool())
			},
		),
		hvue.ComputedWithGetSet("enableNexmon",
			func(vm *hvue.VM) interface{} {
				return vm.Get("settings").Get("nexmon").Bool()
			},
			func(vm *hvue.VM, newValue *js.Object) {
				vm.Get("settings").Set("nexmon", newValue.Bool())
			},
		),

		hvue.Computed("wifiAuthModes", func(vm *hvue.VM) interface{} {
			modes := js.Global.Get("Array").New()
			for val, _ := range pb.WiFiAuthMode_name {
				mode := struct {
					*js.Object
					Label string `js:"label"`
					Value int    `js:"value"`
				}{Object: O()}
				mode.Value = val
				switch pb.WiFiAuthMode(val) {
				case pb.WiFiAuthMode_WPA2_PSK:
					mode.Label = "WPA2"
				case pb.WiFiAuthMode_OPEN:
					mode.Label = "Open"
				default:
					mode.Label = "Unknown"
				}
				modes.Call("push", mode)
			}
			return modes
		}),
		hvue.Computed("wifiModes", func(vm *hvue.VM) interface{} {
			modes := js.Global.Get("Array").New()
			for val, _ := range pb.WiFiWorkingMode_name {
				mode := struct {
					*js.Object
					Label string `js:"label"`
					Value int    `js:"value"`
				}{Object: O()}
				mode.Value = val
				switch pb.WiFiWorkingMode(val) {
				case pb.WiFiWorkingMode_AP:
					mode.Label = "Access Point (AP)"
				case pb.WiFiWorkingMode_STA:
					mode.Label = "Station (Client)"
				case pb.WiFiWorkingMode_STA_FAILOVER_AP:
					mode.Label = "Client with Failover to AP"
				default:
					continue
				}
				modes.Call("push", mode)
			}
			return modes
		}),
		hvue.Computed("mode_ap", func(vm *hvue.VM) interface{} { return pb.WiFiWorkingMode_AP }),
		hvue.Computed("mode_sta", func(vm *hvue.VM) interface{} { return pb.WiFiWorkingMode_STA }),
		hvue.Computed("mode_failover", func(vm *hvue.VM) interface{} { return pb.WiFiWorkingMode_STA_FAILOVER_AP }),
		hvue.Method("reset",
			func(vm *hvue.VM) {
				vm.Get("$store").Call("dispatch", VUEX_ACTION_UPDATE_WIFI_STATE)
			}),
		hvue.Method("deploy",
			func(vm *hvue.VM, wifiSettings *jsWiFiSettings) {
				if f := js.Global.Get("r4dDeployWifi"); f != nil && f != js.Undefined {
					f.Invoke(wifiSettings, js.MakeFunc(func(this *js.Object, args []*js.Object) interface{} {
						vm.Get("$store").Call("dispatch", VUEX_ACTION_DEPLOY_WIFI_SETTINGS, wifiSettings)
						return nil
					}))
					return
				}
				vm.Get("$store").Call("dispatch", VUEX_ACTION_DEPLOY_WIFI_SETTINGS, wifiSettings)
			}),
		hvue.Method("updateStoredSettingsList",
			func(vm *hvue.VM) {
				vm.Store.Call("dispatch", VUEX_ACTION_UPDATE_STORED_WIFI_SETTINGS_LIST)
			}),
		hvue.Method("store",
			func(vm *hvue.VM, name *js.Object) {
				sReq := NewWifiRequestSettingsStorage()
				sReq.TemplateName = name.String()
				sReq.Settings = &jsWiFiSettings{
					Object: vm.Get("$store").Get("state").Get("wifiState").Get("CurrentSettings"),
				}
				println("Storing :", sReq)
				vm.Get("$store").Call("dispatch", VUEX_ACTION_STORE_WIFI_SETTINGS, sReq)
				vm.Set("showStoreModal", false)
			}),
		hvue.Method("load",
			func(vm *hvue.VM, name *js.Object) {
				println("Loading :", name.String())
				vm.Get("$store").Call("dispatch", VUEX_ACTION_LOAD_WIFI_SETTINGS, name)
			}),
		hvue.Method("deployStored",
			func(vm *hvue.VM, name *js.Object) {
				println("Loading :", name.String())
				vm.Get("$store").Call("dispatch", VUEX_ACTION_DEPLOY_STORED_WIFI_SETTINGS, name)
			}),
		hvue.Method("deleteStored",
			func(vm *hvue.VM, name *js.Object) {
				println("Loading :", name.String())
				vm.Get("$store").Call("dispatch", VUEX_ACTION_DELETE_STORED_WIFI_SETTINGS, name)
			}),
		hvue.Computed("deploying",
			func(vm *hvue.VM) interface{} {
				return vm.Get("$store").Get("state").Get("deployingWifiSettings")
			}),
		hvue.Mounted(func(vm *hvue.VM) {
			println("wifi component mounted")
			vm.Store.Call("dispatch", VUEX_ACTION_UPDATE_STORED_WIFI_SETTINGS_LIST)
			vm.Get("$store").Call("dispatch", VUEX_ACTION_UPDATE_WIFI_STATE)
		}),

	)
}

const templateWiFi = `
<q-page padding>
	<select-string-from-array :values="$store.state.StoredWifiSettingsList" v-model="showLoadModal" title="Load WiFi settings" @load="load($event)" @delete="deleteStored($event)" with-delete></select-string-from-array>
	<select-string-from-array :values="$store.state.StoredWifiSettingsList" v-model="showDeployStoredModal" title="Deploy stored WiFi settings" @load="deployStored($event)" @delete="deleteStored($event)" with-delete></select-string-from-array>
	<modal-string-input v-model="showStoreModal" title="Store current WiFi Settings" @save="store($event)"></modal-string-input>

<div class="row gutter-sm">
		<div class="col-12">
			<q-card>
				<q-card-title>
					WiFi settings
				</q-card-title>

				<q-card-main>
					<div class="row gutter-sm">

						<div class="col-6 col-sm""><q-btn :loading="deploying" class="fit" color="primary" @click="deploy(settings)" label="deploy" icon="launch"></q-btn></div>
						<div class="col-6 col-sm""><q-btn class="fit" color="primary" @click="updateStoredSettingsList(); showDeployStoredModal=true" label="deploy stored" icon="settings_backup_restore"></q-btn></div>
						<div class="col-6 col-sm""><q-btn class="fit" color="secondary" @click="reset" label="reset" icon="autorenew"></q-btn></div>
						<div class="col-6 col-sm""><q-btn class="fit" color="secondary" @click="showStoreModal=true" label="store" icon="cloud_upload"></q-btn></div>
						<div class="col-12 col-sm"><q-btn class="fit" color="warning" @click="updateStoredSettingsList(); showLoadModal=true" label="load stored" icon="cloud_download"></q-btn></div>

					</div>
  				</q-card-main>


			</q-card>
		</div>


	<div class="col-12 col-lg">
	<q-card class="full-height">
		<q-card-title>
			Generic
		</q-card-title>

	

		<q-list link>
			<q-item-separator />
			<q-item>
	        	<q-item-side :icon="wifiStateIcon" color="primary"></q-item-side>
				<q-item-main>
					<q-item-tile label>{{ wifiStateText }}</q-item-tile>
				</q-item-main>
			</q-item>

			<q-item-separator />
			<q-item tag="label">
				<q-item-side>
					<q-toggle v-model="enabled"></q-toggle>
				</q-item-side>
				<q-item-main>
					<q-item-tile label>Enabled</q-item-tile>
					<q-item-tile sublabel>Enable/Disable WiFi</q-item-tile>
				</q-item-main>
			</q-item>
<!--
			<q-item tag="label">
				<q-item-side>
					<q-toggle v-model="enableNexmon"></q-toggle>
				</q-item-side>
				<q-item-main>
					<q-item-tile label>Nexmon</q-item-tile>
					<q-item-tile sublabel>Enable/Disable modified nexmon firmware (needed for WiFi covert channel and KARMA)</q-item-tile>
				</q-item-main>
			</q-item>
-->
			<q-item tag="label" disabled>
				<q-item-side>
					<q-toggle :value="true" :disable="true"></q-toggle>
				</q-item-side>
				<q-item-main>
					<q-item-tile label>Nexmon</q-item-tile>
					<q-item-tile sublabel>Enable/Disable modified nexmon firmware (needed for WiFi covert channel and KARMA)</q-item-tile>
				</q-item-main>
			</q-item>
			<q-item tag="label">
				<q-item-main>
					<q-item-tile label>Regulatory domain</q-item-tile>
					<q-item-tile sublabel>Regulatory domain according to ISO/IEC 3166-1 alpha2 (example "US")</q-item-tile>
					<q-item-tile>
						<q-input v-model="settings.reg" inverted></q-input>
					</q-item-tile>
				</q-item-main>
			</q-item>
			<q-item tag="label">
				<q-item-main>
					<q-item-tile label>Working Mode</q-item-tile>
					<q-item-tile sublabel>Work as Access Point or Client</q-item-tile>
					<q-item-tile>
						<q-select v-model="settings.mode" :options="wifiModes" color="secondary" inverted></q-select>
					</q-item-tile>
				</q-item-main>
			</q-item>

		</q-list>
	</q-card>
	</div>

	<div class="col-12 col-lg">
	<q-card class="full-height">
		<q-card-title>
			WiFi client settings
		</q-card-title>

		<q-list link>
				<q-item-separator />
				<q-item>
					<q-item-main>
						<q-item-tile label>Client adapter</q-item-tile>
						<q-item-tile sublabel>On this AP use wlan1 so the page stays up. Use wlan0 only from USB ethernet (stops the AP).</q-item-tile>
						<q-item-tile>
							<select id="r4d-wifi-iface" style="width:100%;padding:8px;background:#eee;border:0">
								<option value="wlan0">wlan0 (onboard)</option>
								<option value="wlan1" selected>wlan1 (USB WiFi)</option>
							</select>
						</q-item-tile>
					</q-item-main>
				</q-item>
				<q-item tag="label">
					<q-item-main>
						<q-item-tile label>SSID</q-item-tile>
						<q-item-tile sublabel>Network name to connect</q-item-tile>
						<q-item-tile>
							<q-input v-model="settings.staBssList[0].ssid" color="primary" inverted></q-input>
						</q-item-tile>
					</q-item-main>
				</q-item>
				<q-item tag="label">
					<q-item-main>
						<q-item-tile label>Pre shared key</q-item-tile>
						<q-item-tile sublabel>If empty, a network with Open Authentication is assumed (Warning: PLAIN TRANSMISSION)</q-item-tile>
						<q-item-tile>
							<q-input v-model="settings.staBssList[0].psk" type="password" color="primary" inverted></q-input>
						</q-item-tile>
					</q-item-main>
				</q-item>
				<q-item>
					<q-item-main>
						<button type="button" id="r4d-wifi-connect" onclick="window.r4dConnectClient && window.r4dConnectClient()" style="width:100%;padding:10px;background:#027be3;color:#fff;border:0;cursor:pointer">Connect as client</button>
						<div id="r4d-wifi-sta-status" style="margin-top:8px;font-size:12px"></div>
					</q-item-main>
				</q-item>

			<template v-if="settings.mode == mode_failover">
				<q-item>
					<q-item-main>
	  				<q-alert type="info">
						Client with Failover to AP uses <b>wlan0</b>: it tries to join the client SSID first. If that fails, it starts the onboard AP. When the AP is up, a USB adapter (wlan1) can still join the same SSID so Share/VPN keep an uplink. Store this in the startup Master Template for boot.
					</q-alert>
					</q-item-main>
				</q-item>
			</template>
		</q-list>
	</q-card>
	</div>

	<div class="col-12 col-lg" v-if="settings.mode == mode_ap || settings.mode == mode_failover">
	<q-card class="full-height">
		<q-card-title>
			WiFi Access Point settings
		</q-card-title>

		<q-list link>


			<template>
				<q-item-separator />
				<q-item tag="label">
					<q-item-main>
						<q-item-tile label>Channel</q-item-tile>
						<q-item-tile sublabel>Must exist in regulatory domain (example 13)</q-item-tile>
						<q-item-tile>
							<q-input v-model="settings.channel" type="number" inverted></q-input>
						</q-item-tile>
					</q-item-main>
				</q-item>

				<q-item tag="label">
					<q-item-main>
						<q-item-tile label>Authentication Mode</q-item-tile>
						<q-item-tile sublabel>Authentication Mode for Access Point (ignored for client mode)</q-item-tile>
						<q-item-tile>
							<q-select v-model="settings.authMode" :options="wifiAuthModes" color="primary" inverted></q-select>
						</q-item-tile>
					</q-item-main>
				</q-item>
				<q-item tag="label">
					<q-item-main>
						<q-item-tile label>SSID</q-item-tile>
						<q-item-tile sublabel>Network name (Service Set Identifier)</q-item-tile>
						<q-item-tile>
							<q-input v-model="settings.apBss.ssid" color="primary" inverted></q-input>
						</q-item-tile>
					</q-item-main>
				</q-item>
				<q-item tag="label">
					<q-item-side>
						<q-toggle v-model="settings.hideSsid"></q-toggle>
					</q-item-side>
					<q-item-main>
						<q-item-tile label>Hide SSID</q-item-tile>
						<q-item-tile sublabel>Access Point doesn't send beacons with its SSID</q-item-tile>
					</q-item-main>
				</q-item>
				<q-item tag="label">
					<q-item-main>
						<q-item-tile label>Pre shared key</q-item-tile>
						<q-item-tile sublabel>Warning: PLAIN TRANSMISSION</q-item-tile>
						<q-item-tile>
							<q-input v-model="settings.apBss.psk" type="password" color="primary" inverted></q-input>
						</q-item-tile>
					</q-item-main>
				</q-item>
			</template>
		</q-list>
	</q-card>
	</div>


</div>
</q-page>	

`
