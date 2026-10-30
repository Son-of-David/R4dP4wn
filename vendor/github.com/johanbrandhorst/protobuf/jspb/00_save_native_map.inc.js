// Preserve the browser's ES6 Map before google-protobuf's webpack bundle
// copies jspb.Map onto the global object. GopherJS 1.18+ uses native Map
// for Go maps; constructing jspb.Map with `new Map()` leaves this.arr_ undefined.
(function (g) {
  if (typeof g.Map === "function") {
    g.__P4WNP1_NATIVE_MAP__ = g.Map;
  }
})(typeof $global !== "undefined" ? $global : typeof window !== "undefined" ? window : this);
