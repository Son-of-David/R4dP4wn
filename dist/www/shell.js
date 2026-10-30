(function () {
  "use strict";

  Vue.component("shell-page", {
    mixins: [window.r4dTermMixin],
    template:
      '<q-page padding>' +
      '<div class="row gutter-sm">' +
      '<div class="col-12">' +
      '<q-card>' +
      '<q-card-title>Terminal</q-card-title>' +
      '<q-card-main>' +
      '<q-alert v-if="err" type="negative" class="q-mb-sm">{{ err }}</q-alert>' +
      '<q-list>' +
      '<q-item>' +
      '<q-item-main>' +
      '<q-item-tile label>Status</q-item-tile>' +
      '<q-item-tile sublabel>{{ statusLine }}</q-item-tile>' +
      '</q-item-main>' +
      '</q-item>' +
      '</q-list>' +
      '<p class="text-grey-7" style="margin-top:8px">Tap the green box or the field below it to type. Computer keyboards work as usual. Phone keyboards type into that field. Use Ctrl+C and the arrows on the bar. Mouse wheel scrolls history.</p>' +
      '<div ref="termBox" tabindex="0" @click="focusTerm" @keydown="onTermKey" @scroll="onTermScroll" @wheel="onTermWheel" :style="termStyle">' +
      '<pre ref="termPre" v-once style="margin:0;padding:12px;min-height:100%;white-space:pre;overflow:visible;font-family:monospace;font-size:13px;line-height:18px;outline:none;color:#b6f2b6">Waiting for shell...</pre>' +
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
        termWsPath: "/api/term/ws",
        status: { running: true, cwd: "/usr/share/S.o.D" },
        err: ""
      };
    },
    computed: {
      statusLine: function () {
        if (this.status && this.status.running) {
          return "bash in /usr/share/S.o.D (" + this.cols + "x" + this.rows + "). Follow is " + (this.follow ? "on" : "off") + ".";
        }
        return "Starting shell...";
      }
    },
    mounted: function () {
      this.bootTerm();
    },
    beforeDestroy: function () {
      this.teardownTerm();
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
      if (routes[i].path === "/terminal") {
        return;
      }
    }
    router.addRoutes([
      { path: "/terminal", name: "terminal", component: { template: "<shell-page></shell-page>" } }
    ]);
  }
  installRoute();
})();
