(function () {
  "use strict";

  var MODE_AP = 1;
  var MODE_STA = 2;
  var MODE_FAILOVER = 3;
  var IFACE_KEY = "r4d-wifi-sta-iface";

  function selectedIface() {
    try {
      return localStorage.getItem(IFACE_KEY) || defaultIface();
    } catch (e) {
      return defaultIface();
    }
  }

  function setSelectedIface(v) {
    try { localStorage.setItem(IFACE_KEY, v); } catch (e) {}
    window.r4dStaIface = v;
  }

  function viaWlan0Ap() {
    var h = location.hostname || "";
    return h === "172.24.0.1" || h.indexOf("172.24.") === 0;
  }

  function viaUsbeth() {
    var h = location.hostname || "";
    return h === "172.16.0.1" || h.indexOf("172.16.") === 0;
  }

  function defaultIface() {
    return viaWlan0Ap() ? "wlan1" : "wlan0";
  }

  function pick(obj, key) {
    if (!obj) { return; }
    if (obj[key] !== undefined && obj[key] !== null) { return obj[key]; }
    try {
      if (obj.Object && obj.Object[key] !== undefined) { return obj.Object[key]; }
    } catch (e) {}
    return;
  }

  function clientBss(settings) {
    var list = pick(settings, "staBssList");
    var first = null;
    if (list) {
      first = list[0];
      if (!first && typeof list.get === "function") {
        try { first = list.get(0); } catch (e) {}
      }
    }
    return {
      ssid: String((first && (pick(first, "ssid") || pick(first, "SSID"))) || "").trim(),
      psk: String((first && (pick(first, "psk") || pick(first, "PSK"))) || "")
    };
  }

  function workingMode(settings) {
    return parseInt(pick(settings, "mode"), 10) || 0;
  }

  function clientCard() {
    var titles = document.querySelectorAll("#app .q-card-title");
    var i;
    for (i = 0; i < titles.length; i++) {
      if ((titles[i].textContent || "").indexOf("WiFi client settings") >= 0) {
        return titles[i].parentNode;
      }
    }
    return null;
  }

  function readClientFieldsFromDom() {
    var card = clientCard();
    var ssid = "";
    var psk = "";
    if (card) {
      var inputs = card.querySelectorAll("input");
      var j;
      for (j = 0; j < inputs.length; j++) {
        var t = (inputs[j].type || "text").toLowerCase();
        if (t === "password") { psk = inputs[j].value; }
        else if (t === "hidden" || t === "checkbox" || t === "number") { continue; }
        else if (!ssid) { ssid = inputs[j].value; }
      }
    }
    return { ssid: String(ssid || "").trim(), psk: String(psk || "") };
  }

  function storeSettings() {
    try {
      return window.vm.$store.state.wifiState.CurrentSettings;
    } catch (e) {
      return null;
    }
  }

  function currentClientBss(settings) {
    var bss = clientBss(settings);
    if (bss.ssid) { return bss; }
    bss = clientBss(storeSettings());
    if (bss.ssid) { return bss; }
    return readClientFieldsFromDom();
  }

  function currentIface() {
    var sel = document.getElementById("r4d-wifi-iface");
    var v = (sel && sel.value) || selectedIface();
    if (v !== "wlan0" && v !== "wlan1") { v = defaultIface(); }
    return v;
  }

  function notify(ok, msg) {
    try {
      if (window.vm && window.vm.$q && window.vm.$q.notify) {
        window.vm.$q.notify({ type: ok ? "positive" : "negative", message: msg, position: "top" });
        return;
      }
    } catch (e) {}
    if (!ok) { window.alert(msg); }
  }

  function setDeploying(v) {
    try { window.vm.$store.state.deployingWifiSettings = !!v; } catch (e) {}
  }

  function joinAp(iface, ssid, psk) {
    return fetch("/api/wifi/join", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ iface: iface, ssid: ssid, psk: psk || "" })
    }).then(function (r) {
      return r.json().then(function (body) {
        if (!r.ok) { throw new Error((body && body.error) || "Join failed"); }
        return body;
      });
    });
  }

  function origDeploy(settings) {
    try {
      return window.vm.$store.dispatch("deployWifiSettings", settings);
    } catch (e) {
      notify(false, "Could not deploy Access Point settings.");
    }
  }

  window.r4dSetIface = function (v) {
    setSelectedIface(v);
  };

  window.r4dConnectClient = function (settings) {
    var bss = currentClientBss(settings);
    if (!bss.ssid) {
      notify(false, "Enter the access point SSID in WiFi client settings.");
      return;
    }
    var iface = currentIface();
    setSelectedIface(iface);
    if (iface === "wlan0" && viaWlan0Ap()) {
      if (!window.confirm("wlan0 as client stops the R4d-P4wn AP and will disconnect this browser. Use wlan1 if you want to stay on the AP. Continue?")) {
        return;
      }
    }
    setDeploying(true);
    joinAp(iface, bss.ssid, bss.psk)
      .then(function (st) {
        var ip = st && st.ip ? " (" + st.ip + ")" : "";
        notify(true, "Connected " + iface + " to '" + bss.ssid + "'" + ip);
        refreshStatus();
      })
      .catch(function (e) {
        notify(false, String(e.message || e));
      })
      .then(function () { setDeploying(false); });
  };

  window.r4dDeployWifi = function (settings, fallback) {
    var mode = workingMode(settings);
    var disabled = !!pick(settings, "disabled");
    if (disabled) {
      if (typeof fallback === "function") { fallback(); }
      else { origDeploy(settings); }
      return;
    }
    if (mode === MODE_STA || mode === MODE_FAILOVER) {
      window.r4dConnectClient(settings);
      return;
    }
    if (typeof fallback === "function") { fallback(); }
    else { origDeploy(settings); }
  };

  function statusText(st) {
    if (st && st.running) {
      var ip = st.ip ? " ip " + st.ip : "";
      return "Client up on " + (st.iface || currentIface()) + " to '" + (st.ssid || "") + "'" + ip + ".";
    }
    if (viaWlan0Ap()) {
      return "You are on the wlan0 AP. Fill SSID/password, leave adapter on wlan1, then Connect as client.";
    }
    if (viaUsbeth()) {
      return "You are on USB ethernet. Pick wlan0 or wlan1, fill SSID/password, then Connect as client.";
    }
    return "Pick adapter, fill SSID and password, then Connect as client.";
  }

  function refreshStatus() {
    var el = document.getElementById("r4d-wifi-sta-status");
    fetch("/api/wifi/status")
      .then(function (r) { return r.json(); })
      .then(function (st) {
        if (el) { el.textContent = statusText(st); }
      })
      .catch(function () {
        if (el) { el.textContent = "Client join API not available. Copy the new P4wnP1_service and restart P4wnP1."; }
      });
  }

  function ensureClientUi() {
    var card = clientCard();
    if (!card) { return; }
    var sel = document.getElementById("r4d-wifi-iface");
    if (!sel) {
      var main = card.querySelector(".q-card-main") || card.querySelector(".q-list") || card;
      var box = document.createElement("div");
      box.style.cssText = "padding:8px 16px 0 16px";
      box.innerHTML =
        '<div class="q-item-label">Client adapter</div>' +
        '<div class="q-item-sublabel" id="r4d-wifi-sta-status" style="margin:4px 0 8px 0"></div>' +
        '<select id="r4d-wifi-iface" style="width:100%;padding:8px;background:#eee;border:0;margin-bottom:8px">' +
        '<option value="wlan0">wlan0 (onboard) — USB ethernet; stops this AP</option>' +
        '<option value="wlan1">wlan1 (USB WiFi) — AP stays up</option>' +
        "</select>" +
        '<button type="button" id="r4d-wifi-connect" style="width:100%;padding:10px;background:#027be3;color:#fff;border:0;cursor:pointer;margin-bottom:8px">Connect as client</button>';
      main.insertBefore(box, main.firstChild);
      sel = box.querySelector("#r4d-wifi-iface");
    }
    if (sel && !sel._r4dBound) {
      sel.value = selectedIface();
      if (viaWlan0Ap() && sel.value !== "wlan1") {
        sel.value = "wlan1";
        setSelectedIface("wlan1");
      }
      sel.addEventListener("change", function () { setSelectedIface(sel.value); });
      sel._r4dBound = true;
    }
    var btn = document.getElementById("r4d-wifi-connect");
    if (btn && !btn._r4dBound) {
      btn.addEventListener("click", function (ev) {
        ev.preventDefault();
        ev.stopPropagation();
        window.r4dConnectClient();
      });
      btn._r4dBound = true;
    }
    if (!document.getElementById("r4d-wifi-sta-status") && card) {
      var st = document.createElement("div");
      st.id = "r4d-wifi-sta-status";
      st.style.cssText = "padding:0 16px 8px 16px;font-size:12px";
      card.appendChild(st);
    }
    refreshStatus();
  }

  function tick() {
    if (!window.vm || !window.vm.$route) { return; }
    var path = window.vm.$route.path || "";
    if (path !== "/wifi" && path.indexOf("wifi") < 0) { return; }
    ensureClientUi();
  }

  setSelectedIface(selectedIface());
  setInterval(tick, 400);
})();
