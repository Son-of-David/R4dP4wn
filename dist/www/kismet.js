(function () {
  "use strict";

  Vue.component("kismet-page", {
    template:
      '<q-page padding>' +
      '<div class="row gutter-sm">' +
      '<div class="col-12 col-lg">' +
      '<q-card>' +
      '<q-card-title>Kismet</q-card-title>' +
      '<q-card-main>' +
      '<q-alert v-if="!status.kismet_bin" type="warning" class="q-mb-sm">Kismet is not installed on this board. Install it with apt-get install kismet, then use this page.</q-alert>' +
      '<q-alert v-if="status.via_wifi" type="warning" class="q-mb-sm">You are connected over the WiFi access point. Starting Kismet on wlan0 will stop that AP and disconnect this browser.</q-alert>' +
      '<q-alert v-if="status.via_usbeth" type="info" class="q-mb-sm">You are connected over USB ethernet. Starting Kismet on wlan0 will drop the WiFi AP, but this page will stay up.</q-alert>' +
      '<q-alert v-if="err" type="negative" class="q-mb-sm">{{ err }}</q-alert>' +
      '<q-list>' +
      '<q-item>' +
      '<q-item-main>' +
      '<q-item-tile label>Status</q-item-tile>' +
      '<q-item-tile sublabel>{{ statusLine }}</q-item-tile>' +
      '</q-item-main>' +
      '</q-item>' +
      '<q-item>' +
      '<q-item-main>' +
      '<q-item-tile label>USB WiFi adapters</q-item-tile>' +
      '<q-item-tile sublabel>{{ usbLine }}</q-item-tile>' +
      '</q-item-main>' +
      '</q-item>' +
      '</q-list>' +
      '<div class="row gutter-sm q-mt-sm">' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!!status.running" class="fit" color="primary" @click="startWlan0" label="Start on wlan0"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!!status.running" class="fit" color="primary" @click="startWlan1" label="Start on wlan1"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!status.running" class="fit" color="negative" @click="stopKismet" label="Kill Kismet"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn class="fit" color="secondary" @click="openUI" label="Open Kismet UI"></q-btn></div>' +
      '</div>' +
      '<p class="q-mt-md text-grey-7" style="margin-top:16px">Start on wlan0 uses the onboard radio and stops the R4d-P4wn AP. Start on wlan1 first renames a USB adapter (wlx…) to wlan1 and never touches wlan0. Kill Kismet deletes leftover monitor interfaces (wlan0mon) and restores the onboard AP if wlan0 was used.</p>' +
      '</q-card-main>' +
      '</q-card>' +
      '</div>' +
      '<div class="col-12 col-lg">' +
      '<q-card>' +
      '<q-card-title>GPS</q-card-title>' +
      '<q-card-main>' +
      '<q-alert type="info" class="q-mb-sm">{{ gpsPipeNote }}</q-alert>' +
      '<q-alert v-if="gpsErr" type="negative" class="q-mb-sm">{{ gpsErr }}</q-alert>' +
      '<q-list>' +
      '<q-item>' +
      '<q-item-main>' +
      '<q-item-tile label>Status</q-item-tile>' +
      '<q-item-tile sublabel>{{ gpsStatusLine }}</q-item-tile>' +
      '</q-item-main>' +
      '</q-item>' +
      '</q-list>' +
      '<div class="row gutter-sm q-mt-sm">' +
      '<div class="col-12 col-sm"><q-btn :loading="gpsBusy" :disable="!!gps.running" class="fit" color="primary" @click="startGps" label="Start GPS"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="gpsBusy" :disable="!gps.running && !gps.viewing" class="fit" color="negative" @click="stopGps" label="Stop GPS"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="gpsBusy" class="fit" color="secondary" @click="viewGps" label="View GPS"></q-btn></div>' +
      '</div>' +
      '<div v-if="showGpsView" style="margin-top:16px">' +
      '<div class="row gutter-sm" style="margin-bottom:8px">' +
      '<div class="col-12 col-sm"><q-btn class="fit" color="primary" @click="hideGps" label="Hide"></q-btn></div>' +
      '</div>' +
      '<div style="height:360px;overflow-y:scroll;overflow-x:auto;-webkit-overflow-scrolling:touch;background:#111;border:1px solid #333">' +
      '<pre style="margin:0;padding:10px;min-height:100%;white-space:pre-wrap;word-break:break-word;color:#b6f2b6;font-family:monospace;font-size:12px;line-height:1.35">{{ gps.output || "Waiting for cgps..." }}</pre>' +
      '</div>' +
      '</div>' +
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
        poll: null,
        gps: {},
        gpsBusy: false,
        gpsErr: "",
        showGpsView: false,
        gpsPoll: null
      };
    },
    computed: {
      statusLine: function () {
        if (this.status.running) {
          return "Running on " + (this.status.iface || "?") + ". Web UI port " + (this.status.ui_port || 2501) + ".";
        }
        return "Kismet is not running.";
      },
      usbLine: function () {
        var list = this.status.usb_wifi || [];
        if (!list.length) {
          return "None detected. Plug a USB adapter into the USB 3.0 host port, then refresh.";
        }
        return list.join(", ") + (this.status.wlan1_present ? " (wlan1 ready)" : " (will be renamed to wlan1 on start)");
      },
      gpsStatusLine: function () {
        if (this.gps.running) {
          return "gpsd is running. " + (this.gps.viewing ? "cgps is open." : "cgps is hidden.");
        }
        return "GPS is stopped.";
      },
      gpsPipeNote: function () {
        var host = (this.gps && this.gps.pipe_host) || window.location.hostname;
        var port = (this.gps && this.gps.pipe_port) || 9999;
        return "GPS data should be piped to " + host + ":" + port;
      }
    },
    mounted: function () {
      this.refresh();
      this.refreshGps();
      this.poll = setInterval(this.refresh, 3000);
      this.gpsPoll = setInterval(this.refreshGps, 1000);
    },
    beforeDestroy: function () {
      if (this.poll) {
        clearInterval(this.poll);
      }
      if (this.gpsPoll) {
        clearInterval(this.gpsPoll);
      }
    },
    methods: {
      refresh: function () {
        var self = this;
        fetch("/api/kismet/status")
          .then(function (r) { return r.json(); })
          .then(function (st) {
            self.status = st || {};
            if (st && st.error) {
              self.err = st.error;
            }
          })
          .catch(function (e) {
            self.err = "Could not reach Kismet API (" + e + "). Restart P4wnP1 after installing this build.";
          });
      },
      refreshGps: function () {
        var self = this;
        fetch("/api/gps/status")
          .then(function (r) { return r.json(); })
          .then(function (st) {
            self.gps = st || {};
            if (st && st.viewing) {
              self.showGpsView = true;
            }
            if (st && st.error && !self.gpsErr) {
              self.gpsErr = st.error;
            }
          })
          .catch(function () {});
      },
      gpsPost: function (path) {
        var self = this;
        self.gpsBusy = true;
        self.gpsErr = "";
        return fetch(path, { method: "POST" })
          .then(function (r) {
            return r.json().then(function (body) {
              return { ok: r.ok, body: body };
            });
          })
          .then(function (res) {
            if (!res.ok) {
              self.gpsErr = (res.body && res.body.error) || "GPS request failed";
            } else {
              self.gps = res.body || self.gps;
            }
          })
          .catch(function (e) {
            self.gpsErr = String(e);
          })
          .then(function () {
            self.gpsBusy = false;
            self.refreshGps();
          });
      },
      startGps: function () {
        this.gpsPost("/api/gps/start");
      },
      stopGps: function () {
        var self = this;
        this.gpsPost("/api/gps/stop").then(function () {
          self.showGpsView = false;
        });
      },
      viewGps: function () {
        var self = this;
        self.showGpsView = true;
        this.gpsPost("/api/gps/view");
      },
      hideGps: function () {
        var self = this;
        this.gpsPost("/api/gps/hide").then(function () {
          self.showGpsView = false;
        });
      },
      startWlan0: function () {
        var msg = "Starting Kismet on wlan0 will stop the R4d-P4wn WiFi access point.";
        if (this.status.via_wifi) {
          msg += "\n\nYou opened this UI over WiFi and WILL BE DISCONNECTED. Prefer USB ethernet at http://172.16.0.1:8000.";
        } else {
          msg += "\n\nAnyone on the WiFi AP will be disconnected. This USB session stays up.";
        }
        if (!window.confirm(msg + "\n\nContinue?")) {
          return;
        }
        this.start("wlan0");
      },
      startWlan1: function () {
        this.start("wlan1");
      },
      start: function (source) {
        var self = this;
        self.busy = true;
        self.err = "";
        fetch("/api/kismet/start", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ source: source })
        })
          .then(function (r) {
            return r.json().then(function (body) {
              return { ok: r.ok, body: body };
            });
          })
          .then(function (res) {
            if (!res.ok) {
              self.err = (res.body && res.body.error) || "Start failed";
            } else {
              self.status = res.body || self.status;
            }
          })
          .catch(function (e) {
            self.err = String(e);
          })
          .then(function () {
            self.busy = false;
            self.refresh();
          });
      },
      stopKismet: function () {
        var self = this;
        self.busy = true;
        self.err = "";
        fetch("/api/kismet/stop", { method: "POST" })
          .then(function (r) { return r.json(); })
          .then(function (st) {
            self.status = st || {};
          })
          .catch(function (e) {
            self.err = String(e);
          })
          .then(function () {
            self.busy = false;
            self.refresh();
          });
      },
      openUI: function () {
        var port = this.status.ui_port || 2501;
        window.open("http://" + window.location.hostname + ":" + port + "/", "_blank");
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
    for (var i = 0; i < routes.length; i++) {
      if (routes[i].path === "/kismet") {
        return;
      }
    }
    router.addRoutes([
      { path: "/kismet", name: "kismet", component: { template: "<kismet-page></kismet-page>" } }
    ]);
  }
  installRoute();
})();
