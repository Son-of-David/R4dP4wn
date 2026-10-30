/*
 * This file is part of R4d-P4wn, a modified version of P4wnP1 A.L.O.A.
 * Copyright (C) 2018 Marcus Mengs (MaMe82)
 * Copyright (C) 2026 Son-of-David
 * SPDX-License-Identifier: GPL-3.0-or-later
 * See LICENSE, NOTICE, and MODIFICATIONS.md in the project root.
 */
(function () {
  "use strict";

  Vue.component("license-page", {
    template:
      '<q-page padding>' +
      '<div class="row gutter-sm">' +
      '<div class="col-12">' +
      '<q-card>' +
      '<q-card-title>License and modifications</q-card-title>' +
      '<q-card-main>' +
      '<p><b>R4d-P4wn</b> is a modified version of <b>P4wnP1 A.L.O.A.</b></p>' +
      '<p>Original program: Copyright (C) 2018 Marcus Mengs (MaMe82).<br>' +
      'This modified version: Copyright (C) 2026 Son-of-David.</p>' +
      '<p>This program is free software: you can redistribute it and/or modify ' +
      'it under the terms of the GNU General Public License as published by ' +
      'the Free Software Foundation, either version 3 of the License, or ' +
      '(at your option) any later version.</p>' +
      '<p>This program is distributed in the hope that it will be useful, ' +
      'but WITHOUT ANY WARRANTY; without even the implied warranty of ' +
      'MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the ' +
      'GNU General Public License for more details.</p>' +
      '<p>You should have received a copy of the GNU General Public License ' +
      'along with this program. If not, see ' +
      '<a href="https://www.gnu.org/licenses/" target="_blank" rel="noopener">https://www.gnu.org/licenses/</a>.</p>' +
      '<p>A list of files added or changed versus upstream P4wnP1 A.L.O.A. ' +
      '(RoganDawes/P4wnP1_aloa commit a56bb4d) is in <code>MODIFICATIONS.md</code> ' +
      'in the source tree. The full GNU GPL text is below (also <a href="LICENSE.txt">LICENSE.txt</a>).</p>' +
      '<pre style="max-height:480px;overflow:auto;white-space:pre-wrap;background:#111;color:#ccc;padding:12px;font-size:12px">{{ text || "Loading LICENSE.txt ..." }}</pre>' +
      '</q-card-main>' +
      '</q-card>' +
      '</div>' +
      '</div>' +
      '</q-page>',
    data: function () {
      return { text: "" };
    },
    mounted: function () {
      var self = this;
      fetch("LICENSE.txt")
        .then(function (r) { return r.text(); })
        .then(function (t) { self.text = t; })
        .catch(function () {
          self.text = "Could not load LICENSE.txt from the web root. See LICENSE in the source tree.";
        });
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
      if (routes[i].path === "/license") { return; }
    }
    router.addRoutes([
      { path: "/license", name: "license", component: { template: "<license-page></license-page>" } }
    ]);
  }
  installRoute();
})();
