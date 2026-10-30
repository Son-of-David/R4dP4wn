// Restore the browser's ES6 Map after google-protobuf has installed BinaryWriter/BinaryReader.
(function (g) {
  if (typeof g.__P4WNP1_NATIVE_MAP__ === "function") {
    g.Map = g.__P4WNP1_NATIVE_MAP__;
    try { delete g.__P4WNP1_NATIVE_MAP__; } catch (e) { g.__P4WNP1_NATIVE_MAP__ = undefined; }
  }
})(typeof $global !== "undefined" ? $global : typeof window !== "undefined" ? window : this);
