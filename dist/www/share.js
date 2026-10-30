(function () {
  "use strict";

  var selStyle = "width:100%;padding:8px;background:#eee;border:0";
  var inpStyle = "width:100%;padding:8px;background:#eee;border:0;box-sizing:border-box";
  var taStyle = "width:100%;min-height:200px;margin-top:8px;padding:10px;font-family:monospace;font-size:12px;background:#111;color:#b6f2b6;border:1px solid #444;box-sizing:border-box";

  Vue.component("share-page", {
    template:
      '<q-page padding>' +
      '<div class="row gutter-sm">' +
      '<div class="col-12 col-lg">' +
      '<q-card>' +
      '<q-card-title>Internet passthrough</q-card-title>' +
      '<q-card-main>' +
      '<q-alert v-if="err" type="negative" class="q-mb-sm">{{ err }}</q-alert>' +
      '<q-alert v-if="status.note" type="info" class="q-mb-sm">{{ status.note }}</q-alert>' +
      '<q-list>' +
      '<q-item><q-item-main><q-item-tile label>Status</q-item-tile><q-item-tile sublabel>{{ shareLine }}</q-item-tile></q-item-main></q-item>' +
      '<q-item><q-item-main><q-item-tile label>wlan0 (onboard)</q-item-tile><q-item-tile sublabel>{{ wlan0Line }}</q-item-tile></q-item-main></q-item>' +
      '<q-item><q-item-main><q-item-tile label>wlan1 (USB WiFi)</q-item-tile><q-item-tile sublabel>{{ wlan1Line }}</q-item-tile></q-item-main></q-item>' +
      '<q-item><q-item-main><q-item-tile label>usbeth</q-item-tile><q-item-tile sublabel>{{ usbLine }}</q-item-tile></q-item-main></q-item>' +
      '<q-item><q-item-main>' +
      '<q-item-tile label>DNS</q-item-tile>' +
      '<q-item-tile sublabel>Applies to USB ethernet (and the AP when uplink is wlan1) for both WiFi passthrough and VPN share. Default is 8.8.8.8, 1.1.1.1.</q-item-tile>' +
      '<q-item-tile>' +
      '<select v-model="dnsMode" style="' + selStyle + '">' +
      '<option value="default">Default (8.8.8.8, 1.1.1.1)</option>' +
      '<option value="off">Off (do not override)</option>' +
      '<option value="custom">Custom</option>' +
      '</select>' +
      '</q-item-tile>' +
      '<q-item-tile v-if="dnsMode === \'custom\'" style="margin-top:8px">' +
      '<input v-model="dnsServers" type="text" placeholder="8.8.8.8, 1.1.1.1" style="' + inpStyle + '">' +
      '</q-item-tile>' +
      '</q-item-main></q-item>' +
      '</q-list>' +
      '<div class="row gutter-sm q-mt-sm">' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!!status.sharing" class="fit" color="primary" @click="startShare(false)" label="Share WiFi to usbeth"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!!status.sharing && !!status.via_vpn" class="fit" color="primary" @click="startShare(true)" label="Share VPN to usbeth"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!status.sharing" class="fit" color="negative" @click="stopShare" label="Stop sharing"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" class="fit" color="secondary" @click="saveDns" label="Save DNS"></q-btn></div>' +
      '</div>' +
      '<p class="text-grey-7" style="margin-top:16px">Uplink is automatic: <b>wlan1</b> if it has an IP (AP can stay up), otherwise <b>wlan0</b> when it is a WiFi client (Client or Client+Failover). Then share that internet to usbeth. Use this page from usbeth at 172.16.0.1. VPN uses the same WiFi uplink.</p>' +
      '</q-card-main>' +
      '</q-card>' +
      '</div>' +
      '<div class="col-12 col-lg">' +
      '<q-card>' +
      '<q-card-title>VPN</q-card-title>' +
      '<q-card-main>' +
      '<q-alert v-if="!status.wg_bin && !status.openvpn_bin" type="warning" class="q-mb-sm">Install a VPN client on the board: apt-get install wireguard-tools and/or openvpn.</q-alert>' +
      '<q-alert v-if="vpnKind === \'openvpn\' && needsAuth && !hasAuth" type="warning" class="q-mb-sm">This profile uses username/password. Enter them below, Save, then Connect. Otherwise OpenVPN waits forever.</q-alert>' +
      '<q-list>' +
      '<q-item><q-item-main><q-item-tile label>Tunnel</q-item-tile><q-item-tile sublabel>{{ vpnLine }}</q-item-tile></q-item-main></q-item>' +
      '<q-item><q-item-main>' +
      '<q-item-tile label>Type</q-item-tile>' +
      '<q-item-tile sublabel>Choose WireGuard or OpenVPN, then pick a saved file from /usr/local/P4wnP1/vpns/</q-item-tile>' +
      '<q-item-tile>' +
      '<select v-model="vpnKind" style="' + selStyle + '">' +
      '<option value="wireguard">WireGuard (*.conf)</option>' +
      '<option value="openvpn">OpenVPN (*.ovpn)</option>' +
      '</select>' +
      '</q-item-tile>' +
      '</q-item-main></q-item>' +
      '<q-item><q-item-main>' +
      '<q-item-tile label>Saved profiles</q-item-tile>' +
      '<q-item-tile sublabel>{{ dirLine }}</q-item-tile>' +
      '<q-item-tile>' +
      '<select v-model="selectedFile" style="' + selStyle + '" @change="onSelectFile">' +
      '<option value="">{{ vpnFiles.length ? "Select a saved file" : "No saved files yet" }}</option>' +
      '<option v-for="f in vpnFiles" :key="f" :value="f">{{ f }}</option>' +
      '</select>' +
      '</q-item-tile>' +
      '</q-item-main></q-item>' +
      '<q-item><q-item-main>' +
      '<q-item-tile label>Save as</q-item-tile>' +
      '<q-item-tile sublabel>{{ saveHint }}</q-item-tile>' +
      '<q-item-tile>' +
      '<input v-model="saveName" type="text" :placeholder="namePlaceholder" style="' + inpStyle + '">' +
      '</q-item-tile>' +
      '</q-item-main></q-item>' +
      '<q-item v-if="vpnKind === \'openvpn\'"><q-item-main>' +
      '<q-item-tile label>Username</q-item-tile>' +
      '<q-item-tile sublabel>Required when the .ovpn has auth-user-pass. Saved next to the profile as a .auth file.</q-item-tile>' +
      '<q-item-tile>' +
      '<input v-model="ovpnUser" type="text" autocomplete="off" placeholder="VPN username" style="' + inpStyle + '">' +
      '</q-item-tile>' +
      '</q-item-main></q-item>' +
      '<q-item v-if="vpnKind === \'openvpn\'"><q-item-main>' +
      '<q-item-tile label>Password</q-item-tile>' +
      '<q-item-tile sublabel>{{ passHint }}</q-item-tile>' +
      '<q-item-tile>' +
      '<input v-model="ovpnPass" type="password" autocomplete="off" :placeholder="passPlaceholder" style="' + inpStyle + '">' +
      '</q-item-tile>' +
      '</q-item-main></q-item>' +
      '<q-item><q-item-main>' +
      '<q-item-tile label>Config</q-item-tile>' +
      '<q-item-tile sublabel>Paste a WireGuard [Interface]/[Peer] file or an OpenVPN client config, then Save. WireGuard IPv6 (::/0) is dropped automatically because IPv6 is disabled on this board. Extra OpenVPN certs can be copied into the same folder.</q-item-tile>' +
      '<q-item-tile>' +
      '<textarea v-model="vpnConfig" spellcheck="false" placeholder="Paste VPN config here" style="' + taStyle + '"></textarea>' +
      '</q-item-tile>' +
      '</q-item-main></q-item>' +
      '</q-list>' +
      '<div class="row gutter-sm q-mt-sm">' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" class="fit" color="secondary" @click="saveVpn" label="Save to vpns/"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!selectedFile" class="fit" color="negative" outline @click="deleteVpn" label="Delete selected"></q-btn></div>' +
      '</div>' +
      '<div class="row gutter-sm q-mt-sm">' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!!status.vpn_running || !selectedFile" class="fit" color="primary" @click="startVpn" label="Connect VPN"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!status.vpn_running" class="fit" color="negative" @click="stopVpn" label="Disconnect VPN"></q-btn></div>' +
      '</div>' +
      '<div style="margin-top:16px">' +
      '<label style="display:block;margin-bottom:8px"><input type="checkbox" v-model="bootVpn" @change="saveBoot"> Connect selected VPN at boot (after WiFi uplink and share are up)</label>' +
      '<label style="display:block"><input type="checkbox" v-model="bootShare" @change="saveBoot"> Share internet at boot (wlan0 STA or wlan1 first, then VPN if that is also enabled)</label>' +
      '</div>' +
      '<p class="text-grey-7" style="margin-top:16px">Connect uses the selected file in /usr/local/P4wnP1/vpns/. For OpenVPN with a login, Save the username and password first. At boot, internet share comes up on wlan1 first; the VPN retries until it handshakes, then share switches to the tunnel. Disconnect on this page stops that retry until the next reboot.</p>' +
      '</q-card-main>' +
      '</q-card>' +
      '</div>' +
      '</div>' +
      '</q-page>',
    data: function () {
      return {
        status: {},
        busy: false,
        err: "",
        vpnKind: "wireguard",
        vpnConfig: "",
        saveName: "",
        selectedFile: "",
        vpnFiles: [],
        ovpnUser: "",
        ovpnPass: "",
        needsAuth: false,
        hasAuth: false,
        bootVpn: false,
        bootShare: false,
        bootSynced: false,
        dnsMode: "default",
        dnsServers: "8.8.8.8,1.1.1.1",
        dnsSynced: false,
        poll: null
      };
    },
    computed: {
      dirLine: function () {
        return (this.status.vpn_dir || "/usr/local/P4wnP1/vpns") + "  (" + (this.vpnFiles.length || 0) + " " + (this.vpnKind === "openvpn" ? ".ovpn" : ".conf") + ")";
      },
      saveHint: function () {
        return this.vpnKind === "openvpn" ? "Saved as name.ovpn" : "Saved as name.conf";
      },
      namePlaceholder: function () {
        return this.vpnKind === "openvpn" ? "home.ovpn" : "home.conf";
      },
      passHint: function () {
        return this.hasAuth ? "A password is already saved for this profile. Type a new one only to replace it." : "Saved with the profile (not shown again).";
      },
      passPlaceholder: function () {
        return this.hasAuth ? "saved — type to replace" : "VPN password";
      },
      shareLine: function () {
        if (this.status.sharing) {
          var via = this.status.via_vpn ? "VPN " : "";
          return "Sharing " + via + (this.status.uplink || "") + (this.status.uplink_ip ? " (" + this.status.uplink_ip + ")" : "") + " to usbeth and wlan0.";
        }
        return "Passthrough is off.";
      },
      wlan0Line: function () {
        if (!this.status.wlan0_present) {
          return "not present";
        }
        if (this.status.wlan0_is_ap) {
          return "AP mode" + (this.status.wlan0_ip ? " (" + this.status.wlan0_ip + ")" : "");
        }
        return this.status.wlan0_ip ? "client, " + this.status.wlan0_ip : "present, no client IP";
      },
      wlan1Line: function () {
        if (this.status.wlan1_present) {
          return this.status.wlan1_ip ? "up, " + this.status.wlan1_ip : "present, no IP (join WiFi as client first)";
        }
        var extra = (this.status.usb_wifi || []).join(", ");
        return extra ? "not named wlan1 yet (" + extra + ")" : "no USB WiFi adapter";
      },
      usbLine: function () {
        return this.status.usbeth_ip ? this.status.usbeth_ip : "down until a USB host is connected";
      },
      vpnLine: function () {
        if (this.status.vpn_running) {
          var file = this.status.vpn_file ? this.status.vpn_file + " " : "";
          return file + (this.status.vpn_kind || "vpn") + " on " + (this.status.vpn_iface || "?") + (this.status.vpn_ip ? " (" + this.status.vpn_ip + ")" : "");
        }
        var tools = [];
        if (this.status.wg_bin) { tools.push("WireGuard"); }
        if (this.status.openvpn_bin) { tools.push("OpenVPN"); }
        return tools.length ? "Disconnected. Ready: " + tools.join(", ") : "Disconnected.";
      }
    },
    watch: {
      vpnKind: function () {
        this.selectedFile = "";
        this.ovpnUser = "";
        this.ovpnPass = "";
        this.needsAuth = false;
        this.hasAuth = false;
        this.refreshList();
      },
      vpnConfig: function (val) {
        this.needsAuth = /(?:^|\n)\s*auth-user-pass\b/i.test(val || "");
      }
    },
    mounted: function () {
      this.refresh();
      this.refreshList();
      this.poll = setInterval(this.refresh, 3000);
    },
    beforeDestroy: function () {
      if (this.poll) { clearInterval(this.poll); }
    },
    methods: {
      refresh: function () {
        var self = this;
        fetch("/api/share/status")
          .then(function (r) { return r.json(); })
          .then(function (st) {
            self.status = st || {};
            self.bootVpn = !!(st && st.boot_vpn);
            self.bootShare = !!(st && st.boot_share);
            if (!self.dnsSynced) {
              self.dnsSynced = true;
              self.dnsMode = (st && st.dns_mode) ? st.dns_mode : "default";
              self.dnsServers = (st && st.dns) ? st.dns : "8.8.8.8,1.1.1.1";
            }
            if (!self.bootSynced && st && st.boot_vpn_name) {
              self.bootSynced = true;
              if (st.boot_vpn_kind) { self.vpnKind = st.boot_vpn_kind; }
              self.selectedFile = st.boot_vpn_name;
            }
            self.refreshList();
          })
          .catch(function (e) {
            self.err = "Could not reach Share API (" + e + "). Restart P4wnP1 after installing this build.";
          });
      },
      refreshList: function () {
        var self = this;
        fetch("/api/share/vpn/list?kind=" + encodeURIComponent(this.vpnKind))
          .then(function (r) { return r.json(); })
          .then(function (body) {
            self.vpnFiles = (body && body.files) || [];
            if (self.selectedFile && self.vpnFiles.indexOf(self.selectedFile) < 0) {
              self.selectedFile = "";
            }
          })
          .catch(function () { self.vpnFiles = []; });
      },
      onSelectFile: function () {
        var self = this;
        if (!self.selectedFile) { return; }
        self.saveName = self.selectedFile;
        self.ovpnPass = "";
        fetch("/api/share/vpn/config?kind=" + encodeURIComponent(self.vpnKind) + "&name=" + encodeURIComponent(self.selectedFile))
          .then(function (r) { return r.json(); })
          .then(function (body) {
            if (body && body.config) {
              self.vpnConfig = body.config;
            }
            self.needsAuth = !!(body && body.needs_auth);
            self.hasAuth = !!(body && body.has_auth);
            if (body && body.username) {
              self.ovpnUser = body.username;
            }
            if (body && body.error) {
              self.err = body.error;
            }
          })
          .catch(function (e) { self.err = String(e); });
      },
      post: function (path, body) {
        var self = this;
        self.busy = true;
        self.err = "";
        return fetch(path, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(body || {})
        })
          .then(function (r) {
            return r.json().then(function (j) { return { ok: r.ok, body: j }; });
          })
          .then(function (res) {
            if (!res.ok) {
              self.err = (res.body && res.body.error) || "Request failed";
            }
            return res;
          })
          .catch(function (e) {
            self.err = String(e);
            return { ok: false, body: {} };
          })
          .then(function (res) {
            self.busy = false;
            return res;
          });
      },
      saveBoot: function () {
        var self = this;
        var name = self.selectedFile || (self.status && self.status.boot_vpn_name) || "";
        var kind = self.vpnKind || (self.status && self.status.boot_vpn_kind) || "wireguard";
        if (self.bootVpn && !name) {
          self.err = "Select a saved VPN file before enabling connect at boot.";
          self.bootVpn = false;
          return;
        }
        fetch("/api/share/boot", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            vpn_enable: !!self.bootVpn,
            vpn_kind: kind,
            vpn_name: name,
            share_enable: !!self.bootShare,
            share_via_vpn: !!self.bootVpn,
            dns_mode: self.dnsMode || "default",
            dns: self.dnsServers || "8.8.8.8,1.1.1.1"
          })
        })
          .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, body: j }; }); })
          .then(function (res) {
            if (!res.ok) {
              self.err = (res.body && res.body.error) || "Could not save boot settings";
              return;
            }
            if (res.body) {
              self.status.boot_vpn = res.body.vpn_enable;
              self.status.boot_vpn_kind = res.body.vpn_kind;
              self.status.boot_vpn_name = res.body.vpn_name;
              self.status.boot_share = res.body.share_enable;
              self.status.boot_share_via_vpn = res.body.share_via_vpn;
              if (res.body.dns_mode) { self.dnsMode = res.body.dns_mode; }
              if (res.body.dns) { self.dnsServers = res.body.dns; }
            }
          })
          .catch(function (e) { self.err = String(e); });
      },
      saveDns: function () {
        var self = this;
        if (self.dnsMode === "custom" && !String(self.dnsServers || "").trim()) {
          self.err = "Enter at least one IPv4 DNS server, for example 8.8.8.8,1.1.1.1";
          return;
        }
        this.saveBoot();
      },
      startShare: function (viaVpn) {
        var self = this;
        this.post("/api/share/start", { via_vpn: !!viaVpn }).then(function (res) {
          if (res && res.ok && res.body) { self.status = res.body; }
        });
      },
      stopShare: function () {
        var self = this;
        this.post("/api/share/stop", {}).then(function (res) {
          if (res && res.ok && res.body) { self.status = res.body; }
        });
      },
      saveVpn: function () {
        var self = this;
        if (!String(self.saveName || "").trim()) {
          self.err = "Enter a file name to save as (for example home.conf or work.ovpn).";
          return;
        }
        if (!String(self.vpnConfig || "").trim()) {
          self.err = "Paste a VPN config into the box first.";
          return;
        }
        if (self.vpnKind === "openvpn" && self.needsAuth && !String(self.ovpnUser || "").trim() && !self.hasAuth) {
          self.err = "This OpenVPN profile needs a username and password.";
          return;
        }
        if (self.vpnKind === "openvpn" && String(self.ovpnUser || "").trim() && !String(self.ovpnPass || "") && !self.hasAuth) {
          self.err = "Enter the OpenVPN password too, then Save.";
          return;
        }
        this.post("/api/share/vpn/save", {
          kind: self.vpnKind,
          name: self.saveName,
          config: self.vpnConfig,
          username: self.ovpnUser || "",
          password: self.ovpnPass || ""
        }).then(function (res) {
          if (!res || !res.ok) { return; }
          self.vpnFiles = (res.body && res.body.files) || self.vpnFiles;
          if (res.body && res.body.name) {
            self.selectedFile = res.body.name;
            self.saveName = res.body.name;
          }
          if (self.ovpnPass) {
            self.hasAuth = true;
            self.ovpnPass = "";
          }
          self.refreshList();
        });
      },
      deleteVpn: function () {
        var self = this;
        if (!self.selectedFile) { return; }
        if (!window.confirm("Delete " + self.selectedFile + " from /usr/local/P4wnP1/vpns/?")) { return; }
        this.post("/api/share/vpn/delete", {
          kind: self.vpnKind,
          name: self.selectedFile
        }).then(function (res) {
          if (!res || !res.ok) { return; }
          self.selectedFile = "";
          self.hasAuth = false;
          self.needsAuth = false;
          self.ovpnUser = "";
          self.ovpnPass = "";
          self.vpnFiles = (res.body && res.body.files) || [];
          self.refreshList();
        });
      },
      startVpn: function () {
        var self = this;
        if (!self.selectedFile) {
          self.err = "Save the config, then select it from the list before connecting.";
          return;
        }
        if (self.vpnKind === "openvpn" && self.needsAuth && !self.hasAuth && !(String(self.ovpnUser || "").trim() && String(self.ovpnPass || ""))) {
          self.err = "Enter OpenVPN username and password, Save, then Connect.";
          return;
        }
        this.post("/api/share/vpn/start", {
          kind: self.vpnKind,
          name: self.selectedFile,
          username: self.ovpnUser || "",
          password: self.ovpnPass || ""
        }).then(function (res) {
          if (res && res.ok && res.body) { self.status = res.body; }
          if (res && res.ok) {
            self.ovpnPass = "";
            if (self.ovpnUser) { self.hasAuth = true; }
            if (self.bootVpn) { self.saveBoot(); }
          }
        });
      },
      stopVpn: function () {
        var self = this;
        this.post("/api/share/vpn/stop", {}).then(function (res) {
          if (res && res.ok && res.body) { self.status = res.body; }
        });
      }
    }
  });

  function installRoute() {
    if (!window.vm || !window.vm.$router) {
      setTimeout(installRoute, 50);
      return;
    }
    var router = window.vm.$router;
    var routes = (router.options && router.options.routes) || [];
    var i;
    for (i = 0; i < routes.length; i++) {
      if (routes[i].path === "/share") { return; }
    }
    router.addRoutes([
      { path: "/share", name: "share", component: { template: "<share-page></share-page>" } }
    ]);
  }
  installRoute();
})();
