(function () {
  "use strict";

  var selStyle = "width:100%;padding:8px;background:#eee;border:0";
  var inpStyle = "width:100%;padding:8px;background:#eee;border:0;box-sizing:border-box";

  Vue.component("capture-page", {
    template:
      '<q-page padding>' +
      '<div class="row gutter-sm">' +
      '<div class="col-12 col-lg">' +
      '<q-card>' +
      '<q-card-title>Packet capture</q-card-title>' +
      '<q-card-main>' +
      '<q-alert v-if="!status.tshark_bin && !status.tcpdump_bin" type="warning" class="q-mb-sm">Install capture tools on the board: apt-get install tshark tcpdump</q-alert>' +
      '<q-alert v-if="err" type="negative" class="q-mb-sm">{{ err }}</q-alert>' +
      '<q-alert v-if="status.error" type="negative" class="q-mb-sm">{{ status.error }}</q-alert>' +
      '<q-alert v-if="status.note" type="info" class="q-mb-sm">{{ status.note }}</q-alert>' +
      '<q-list>' +
      '<q-item><q-item-main><q-item-tile label>Status</q-item-tile><q-item-tile sublabel>{{ statusLine }}</q-item-tile></q-item-main></q-item>' +
      '<q-item><q-item-main>' +
      '<q-item-tile label>Tool</q-item-tile>' +
      '<q-item-tile>' +
      '<select v-model="tool" style="' + selStyle + '">' +
      '<option value="tshark">tshark</option>' +
      '<option value="tcpdump">tcpdump</option>' +
      '</select>' +
      '</q-item-tile>' +
      '</q-item-main></q-item>' +
      '<q-item><q-item-main>' +
      '<q-item-tile label>Interface</q-item-tile>' +
      '<q-item-tile>' +
      '<select v-model="iface" style="' + selStyle + '">' +
      '<option v-for="i in (status.ifaces || [])" :key="i.name" :value="i.name">{{ ifaceLabel(i) }}</option>' +
      '</select>' +
      '</q-item-tile>' +
      '</q-item-main></q-item>' +
      '<q-item><q-item-main>' +
      '<q-item-tile label>Capture filter (BPF)</q-item-tile>' +
      '<q-item-tile sublabel>tcpdump/libpcap syntax, for example: port 80 or not arp</q-item-tile>' +
      '<q-item-tile><input v-model="bpf" type="text" placeholder="optional" style="' + inpStyle + '"></q-item-tile>' +
      '</q-item-main></q-item>' +
      '<q-item><q-item-main>' +
      '<q-item-tile label>Save as</q-item-tile>' +
      '<q-item-tile sublabel>Written to /usr/local/P4wnP1/captures/ as name.pcapng. Leave blank for a timestamped name.</q-item-tile>' +
      '<q-item-tile><input v-model="saveName" type="text" placeholder="capture-name" style="' + inpStyle + '"></q-item-tile>' +
      '</q-item-main></q-item>' +
      '<q-item><q-item-main>' +
      '<q-item-tile label>Limits</q-item-tile>' +
      '<q-item-tile>' +
      '<div class="row gutter-sm">' +
      '<div class="col-4"><input v-model.number="snaplen" type="number" min="0" placeholder="snaplen" title="Snaplen bytes, 0 = default" style="' + inpStyle + '"></div>' +
      '<div class="col-4"><input v-model.number="count" type="number" min="0" placeholder="packets" title="Stop after N packets, 0 = unlimited" style="' + inpStyle + '"></div>' +
      '<div class="col-4"><input v-model.number="duration" type="number" min="0" placeholder="seconds" title="Stop after N seconds, 0 = unlimited" style="' + inpStyle + '"></div>' +
      '</div>' +
      '</q-item-tile>' +
      '</q-item-main></q-item>' +
      '<q-item><q-item-main>' +
      '<q-item-tile>' +
      '<label style="display:block;margin-bottom:6px"><input type="checkbox" v-model="promisc"> Promiscuous</label>' +
      '<label style="display:block;margin-bottom:6px"><input type="checkbox" v-model="monitor"> Monitor mode (WiFi; wlan0 stops the AP)</label>' +
      '<label style="display:block"><input type="checkbox" v-model="resolve"> Resolve names</label>' +
      '</q-item-tile>' +
      '</q-item-main></q-item>' +
      '<q-item><q-item-main>' +
      '<q-item-tile label>Extra arguments</q-item-tile>' +
      '<q-item-tile sublabel>Passed directly to tshark or tcpdump (not a shell). Applied on Start and when reading a saved file.</q-item-tile>' +
      '<q-item-tile><input v-model="extra" type="text" placeholder="optional extra flags" style="' + inpStyle + '"></q-item-tile>' +
      '</q-item-main></q-item>' +
      '</q-list>' +
      '<div class="row gutter-sm q-mt-sm">' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!!status.running" class="fit" color="primary" @click="startCap" label="Start capture"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn :loading="busy" :disable="!status.running" class="fit" color="negative" @click="stopCap" label="Stop and save"></q-btn></div>' +
      '<div class="col-12 col-sm"><q-btn class="fit" color="secondary" @click="toggleHelp" :label="showHelp ? \'Hide \' + tool + \' -h\' : tool + \' -h\'"></q-btn></div>' +
      '</div>' +
      '<pre v-if="showHelp" style="margin-top:12px;max-height:240px;overflow:auto;padding:10px;background:#111;color:#ccc;font-size:11px">{{ helpText || "Loading..." }}</pre>' +
      '</q-card-main>' +
      '</q-card>' +
      '</div>' +
      '<div class="col-12 col-lg">' +
      '<q-card>' +
      '<q-card-title>Saved pcaps in /usr/local/P4wnP1/captures/</q-card-title>' +
      '<q-card-main>' +
      '<q-item>' +
      '<q-item-main>' +
      '<q-item-tile label>Display filter (for Read)</q-item-tile>' +
      '<q-item-tile sublabel>Wireshark syntax when reading a saved file with tshark, for example: http or dns</q-item-tile>' +
      '<q-item-tile><input v-model="displayFilter" type="text" placeholder="optional" style="' + inpStyle + '"></q-item-tile>' +
      '</q-item-main>' +
      '</q-item>' +
      '<q-list v-if="files.length">' +
      '<q-item v-for="f in files" :key="f.name">' +
      '<q-item-main>' +
      '<q-item-tile label>{{ f.name }}</q-item-tile>' +
      '<q-item-tile sublabel>{{ fileLine(f) }}</q-item-tile>' +
      '</q-item-main>' +
      '<q-item-side>' +
      '<q-btn dense color="primary" @click="readFile(f.name)" label="Read"></q-btn> ' +
      '<q-btn dense color="secondary" @click="downloadFile(f.name)" label="Download"></q-btn> ' +
      '<q-btn dense color="negative" outline @click="deleteFile(f.name)" label="Delete"></q-btn>' +
      '</q-item-side>' +
      '</q-item>' +
      '</q-list>' +
      '<p v-else class="text-grey-7">No saved captures yet.</p>' +
      '<div v-if="readOut" style="margin-top:12px">' +
      '<div style="max-height:320px;overflow:auto;background:#111;border:1px solid #333">' +
      '<pre style="margin:0;padding:10px;color:#b6f2b6;font-family:monospace;font-size:12px;white-space:pre-wrap">{{ readOut }}</pre>' +
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
        files: [],
        busy: false,
        err: "",
        tool: "tshark",
        iface: "any",
        bpf: "",
        displayFilter: "",
        saveName: "",
        extra: "",
        snaplen: 0,
        count: 0,
        duration: 0,
        promisc: true,
        monitor: false,
        resolve: false,
        poll: null,
        showHelp: false,
        helpText: "",
        readOut: ""
      };
    },
    computed: {
      statusLine: function () {
        if (this.status.running) {
          var size = "";
          if (this.status.pcap_size) {
            size = " — " + this.fmtSize(this.status.pcap_size);
          }
          return (this.status.tool || "capture") + " on " + (this.status.iface || "?") +
            (this.status.pcap ? " writing " + this.status.pcap : "") +
            size +
            (this.status.started_ago ? " (" + this.status.started_ago + ")" : "");
        }
        var tools = [];
        if (this.status.tshark_bin) { tools.push("tshark"); }
        if (this.status.tcpdump_bin) { tools.push("tcpdump"); }
        return tools.length ? "Idle. Ready: " + tools.join(", ") : "Idle.";
      }
    },
    mounted: function () {
      this.refresh();
      this.refreshFiles();
      this.poll = setInterval(this.pollTick, 2000);
    },
    beforeDestroy: function () {
      if (this.poll) { clearInterval(this.poll); }
    },
    methods: {
      ifaceLabel: function (i) {
        var bits = [i.name];
        if (i.wireless) { bits.push("wifi"); }
        bits.push(i.up ? "up" : "down");
        return bits.join(" — ");
      },
      fmtSize: function (n) {
        n = Number(n) || 0;
        if (n < 1024) { return n + " B"; }
        if (n < 1024 * 1024) { return (n / 1024).toFixed(1) + " KB"; }
        return (n / (1024 * 1024)).toFixed(2) + " MB";
      },
      fileLine: function (f) {
        return this.fmtSize(f.size) + "  " + (f.mtime || "");
      },
      pollTick: function () {
        this.refresh();
        if (this.status.running) {
          this.refreshFiles();
        }
      },
      refresh: function () {
        var self = this;
        fetch("/api/capture/status")
          .then(function (r) { return r.json(); })
          .then(function (st) {
            self.status = st || {};
            if (!self.iface && st && st.ifaces && st.ifaces.length) {
              self.iface = st.ifaces[0].name;
            }
          })
          .catch(function (e) {
            self.err = "Could not reach Capture API (" + e + "). Restart P4wnP1 after installing this build.";
          });
      },
      refreshFiles: function () {
        var self = this;
        fetch("/api/capture/files")
          .then(function (r) { return r.json(); })
          .then(function (body) { self.files = (body && body.files) || []; })
          .catch(function () { self.files = []; });
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
      startCap: function () {
        var self = this;
        this.post("/api/capture/start", {
          tool: this.tool,
          iface: this.iface,
          bpf: this.bpf,
          name: this.saveName,
          extra: this.extra,
          snaplen: Number(this.snaplen) || 0,
          count: Number(this.count) || 0,
          duration: Number(this.duration) || 0,
          promisc: this.promisc,
          monitor: this.monitor,
          resolve: this.resolve
        }).then(function (res) {
          if (res && res.ok && res.body) {
            self.status = res.body;
          }
          self.refreshFiles();
        });
      },
      stopCap: function () {
        var self = this;
        this.post("/api/capture/stop", {}).then(function (res) {
          if (res && res.ok && res.body) { self.status = res.body; }
          self.refreshFiles();
        });
      },
      toggleHelp: function () {
        var self = this;
        self.showHelp = !self.showHelp;
        if (!self.showHelp) { return; }
        fetch("/api/capture/help?tool=" + encodeURIComponent(this.tool))
          .then(function (r) { return r.json(); })
          .then(function (body) { self.helpText = (body && body.help) || ""; })
          .catch(function (e) { self.helpText = String(e); });
      },
      downloadFile: function (name) {
        window.location = "/api/capture/download/" + encodeURIComponent(name);
      },
      deleteFile: function (name) {
        var self = this;
        if (!window.confirm("Delete " + name + "?")) { return; }
        this.post("/api/capture/delete", { name: name }).then(function (res) {
          if (res && res.ok) {
            self.files = (res.body && res.body.files) || [];
            if (self.readOut) { self.readOut = ""; }
          }
        });
      },
      readFile: function (name) {
        var self = this;
        this.post("/api/capture/read", {
          name: name,
          display_filter: self.displayFilter,
          extra: self.extra
        }).then(function (res) {
          if (res && res.ok) {
            self.readOut = (res.body && res.body.output) || "";
          }
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
      if (routes[i].path === "/capture") { return; }
    }
    router.addRoutes([
      { path: "/capture", name: "capture", component: { template: "<capture-page></capture-page>" } }
    ]);
  }
  installRoute();
})();
