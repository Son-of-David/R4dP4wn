const webpack = require("webpack");

// libraryTarget "this" copies google-protobuf exports onto the global object,
// including jspb.Map as `Map`. That overwrites the native ES6 Map GopherJS uses
// for Go maps and crashes with: can't access property "length", this.arr_ is undefined.
const preserveNativeMap = {
    apply(compiler) {
        compiler.hooks.emit.tap("PreserveNativeMap", (compilation) => {
            Object.keys(compilation.assets).forEach((name) => {
                if (!name.endsWith("jspb.inc.js")) {
                    return;
                }
                const src = compilation.assets[name].source();
                const oldWrap = "!function(e,t){for(var r in t)e[r]=t[r]}(this,";
                const newWrap = "!function(e,t){var n=e.Map;for(var r in t)e[r]=t[r];if(n)e.Map=n}(this,";
                if (src.indexOf(oldWrap) === -1) {
                    return;
                }
                const patched = src.replace(oldWrap, newWrap);
                compilation.assets[name] = {
                    source() { return patched; },
                    size() { return patched.length; },
                };
            });
        });
    },
};

module.exports = {
    entry: "./node_modules/google-protobuf/google-protobuf.js",
    output: {
        filename: "jspb.inc.js",
        libraryTarget: "this",
        path: __dirname,
    },
    optimization: {
        minimize: true
    },
    mode: "production",
    plugins: [preserveNativeMap],
};
