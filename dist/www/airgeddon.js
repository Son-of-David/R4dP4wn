(function () {
  "use strict";

  Vue.component("airgeddon-page", {
    mixins: [window.r4dTermMixin],
    template:
      '<q-page padding>' +
      '<div class="row gutter-sm">' +
      '<div class="col-12">' +
      '<q-card>' +
      '<q-card-title>Airgeddon</q-card-title>' +
      '<q-card-main>' +
      '<q-alert v-if="!status.airgeddon_bin" type="warning" class="q-mb-sm">Airgeddon is not installed on this board.</q-alert>' +
      '<q-alert v-if="err" type="negative" class="q-mb-sm">{{ err }}</q-alert>' +
      '<q-alert v-if="note" type="info" class="q-mb-sm">{{ note }}</q-alert>' +
      '<q-list>' +
      '<q-item>' +
      '<q-item-main>' +
      '<q-item-tile label>Status</q-item-tile>' +
      '<q-item-tile sublabel>{{ statusLine }}</q-item-tile>' +
      '</q-item-main>' +
      '</q-item>' +
      '</q-list>' +
      '<div class="row gutter-sm q-mt-sm">' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!!status.running" class="fit" color="primary" @click="startAir" label="Start Airgeddon"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!status.running" class="fit" color="negative" @click="killAir" label="Kill Airgeddon"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" class="fit" color="secondary" @click="restoreWlan0" label="Restore wlan0"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!!status.running" class="fit" color="secondary" @click="wlan0Monitor" label="wlan0 monitor mode"></q-btn></div>' +
      '</div>' +
      '<p class="text-grey-7" style="margin-top:16px">This view attaches to airgeddon\'s tmux session. Over USB ethernet (172.16.0.1), tap wlan0 monitor mode before Start so airgeddon can use the onboard radio. That runs airmon-ng check kill and airmon-ng start wlan0 (stops the AP, USB stays up). Tap the field below the terminal to type. Ctrl+C and the arrows are on the bar.</p>' +
      '<div ref="termBox" tabindex="0" @click="focusTerm" @keydown="onTermKey" @scroll="onTermScroll" @wheel="onTermWheel" :style="termStyle">' +
      '<pre ref="termPre" v-once style="margin:0;padding:12px;min-height:100%;white-space:pre;overflow:visible;font-family:monospace;font-size:13px;line-height:18px;outline:none;color:#b6f2b6">Airgeddon terminal will appear here after Start.</pre>' +
      '</div>' +
      window.r4dTermInputHtml +
      '<div class="row gutter-sm" style="margin-top:12px">' +
      '<div class="col-12"><q-btn class="fit" :color="follow ? \'secondary\' : \'dark\'" @click="toggleFollow" :label="follow ? \'Follow output: on\' : \'Follow output: off\'"></q-btn></div>' +
      '</div>' +
      '</q-card-main>' +
      '</q-card>' +
      '</div>' +
      '</div>' +
      '</q-page>',
    data: function () {
      return {
        termWsPath: "/api/airgeddon/ws",
        status: {},
        busy: false,
        err: "",
        note: "",
        poll: null
      };
    },
    computed: {
      statusLine: function () {
        var mon = this.status.monitor ? " Monitor: " + this.status.monitor + "." : "";
        if (this.status.running) {
          var sess = this.status.session ? this.status.session : "tmux";
          return "Attached to " + sess + " (" + this.cols + "x" + this.rows + ")." + mon;
        }
        return "Airgeddon is not running." + mon;
      }
    },
    mounted: function () {
      this.bootTerm();
      this.refresh();
      this.poll = setInterval(this.refresh, 5000);
    },
    beforeDestroy: function () {
      if (this.poll) { clearInterval(this.poll); }
      this.teardownTerm();
    },
    methods: {
      refresh: function () {
        var self = this;
        fetch("/api/airgeddon/status")
          .then(function (r) { return r.json(); })
          .then(function (st) {
            var running = self.status.running;
            st = st || {};
            if (self.status.running !== st.running || self.status.session !== st.session || self.status.monitor !== st.monitor || self.status.airgeddon_bin !== st.airgeddon_bin) {
              self.status = st;
            }
            if (st.error) { self.err = st.error; }
            if (st.running && !running) {
              self.connectTerm();
            }
          })
          .catch(function (e) {
            self.err = "Could not reach Airgeddon API (" + e + "). Restart P4wnP1 after installing this build.";
          });
      },
      post: function (path) {
        var self = this;
        self.busy = true;
        self.err = "";
        self.note = "";
        return fetch(path, { method: "POST" })
          .then(function (r) {
            return r.json().then(function (body) {
              return { ok: r.ok, body: body };
            });
          })
          .then(function (res) {
            if (!res.ok) {
              self.err = (res.body && res.body.error) || "Request failed";
            } else if (res.body && res.body.note) {
              self.note = res.body.note;
              self.status = Object.assign({}, self.status, res.body);
            } else if (res.body) {
              self.status = res.body;
            }
          })
          .catch(function (e) {
            self.err = String(e);
          })
          .then(function () {
            self.busy = false;
            self.refresh();
            self.connectTerm();
          });
      },
      startAir: function () {
        var self = this;
        this.post("/api/airgeddon/start").then(function () {
          self.$nextTick(function () {
            self.focusTermInput();
          });
        });
      },
      killAir: function () {
        this.post("/api/airgeddon/stop");
      },
      restoreWlan0: function () {
        var msg = "Restore wlan0 will delete leftover monitor interfaces (wlan0mon) and restart P4wnP1. USB ethernet will drop. Continue?";
        if (!window.confirm(msg)) { return; }
        this.post("/api/airgeddon/restore");
      },
      wlan0Monitor: function () {
        var viaAp = (location.hostname || "").indexOf("172.24.") === 0;
        var msg = viaAp
          ? "This stops the R4d-P4wn AP (airmon-ng check kill; airmon-ng start wlan0) and will disconnect this WiFi page. Use USB ethernet at http://172.16.0.1:8000 if you can. Continue?"
          : "Stop the onboard AP and run airmon-ng check kill; airmon-ng start wlan0? USB ethernet stays up. Continue?";
        if (!window.confirm(msg)) { return; }
        this.post("/api/airgeddon/monitor");
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
      if (routes[i].path === "/airgeddon") {
        return;
      }
    }
    router.addRoutes([
      { path: "/airgeddon", name: "airgeddon", component: { template: "<airgeddon-page></airgeddon-page>" } }
    ]);
  }
  installRoute();
})();
