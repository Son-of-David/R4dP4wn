#!/usr/bin/env python3
"""Keep GopherJS on the native ES6 Map when google-protobuf exports jspb.Map.

Also apply the GopherJS 1.18.0-beta3 $externalize nil-map/nil-slice fixes.
GopherJS 1.18-beta2 represents a nil map as `false`; calling v.keys() then
throws "v.keys is not a function" on every RPC that passes nil metadata.
"""

from __future__ import annotations

import sys
from pathlib import Path

OLD_WRAP = "!function(e,t){for(var r in t)e[r]=t[r]}(this,"
NEW_WRAP = "!function(e,t){var n=e.Map;for(var r in t)e[r]=t[r];if(n)e.Map=n}(this,"

OLD_MAP_EXT = """  case $kindMap:
    var m = {};
    var keys = Array.from(v.keys());"""

NEW_MAP_EXT = """  case $kindMap:
    if (v.keys === undefined) {
      return null;
    }
    var m = {};
    var keys = Array.from(v.keys());"""

OLD_SLICE_EXT = """  case $kindSlice:
    if ($needsExternalization(t.elem)) {
      return $mapArray($sliceToNativeArray(v), function(e) { return $externalize(e, t.elem, makeWrapper); });
    }
    return $sliceToNativeArray(v);"""

NEW_SLICE_EXT = """  case $kindSlice:
    if (v === v.constructor.nil) {
      return null;
    }
    if ($needsExternalization(t.elem)) {
      return $mapArray($sliceToNativeArray(v), function(e) { return $externalize(e, t.elem, makeWrapper); });
    }
    return $sliceToNativeArray(v);"""


def patch_webpack_bootstrap(text: str) -> tuple[str, int]:
    count = text.count(OLD_WRAP)
    if count:
        text = text.replace(OLD_WRAP, NEW_WRAP)
    return text, count


def capture_native_map(text: str) -> str:
    """Bind $nativeMap at GopherJS runtime init, before package .inc.js files run."""
    if "var $nativeMap = $global.Map;" in text:
        runtime, sep, rest = text.partition(NEW_WRAP if NEW_WRAP in text else OLD_WRAP)
        if "new Map()" in runtime:
            runtime = runtime.replace("new Map()", "new $nativeMap()")
            text = runtime + sep + rest
        return text

    needle = "var $makeMap = function(keyForFunc, entries) {"
    replacement = "var $nativeMap = $global.Map; var $makeMap = function(keyForFunc, entries) {"
    if needle not in text:
        raise SystemExit("could not find $makeMap in GopherJS runtime")
    text = text.replace(needle, replacement, 1)

    runtime, sep, rest = text.partition(NEW_WRAP if NEW_WRAP in text else OLD_WRAP)
    runtime = runtime.replace("new Map()", "new $nativeMap()")
    return runtime + sep + rest


def patch_nil_externalize(text: str) -> tuple[str, int]:
    n = 0
    if OLD_MAP_EXT in text:
        text = text.replace(OLD_MAP_EXT, NEW_MAP_EXT, 1)
        n += 1
    if OLD_SLICE_EXT in text:
        text = text.replace(OLD_SLICE_EXT, NEW_SLICE_EXT, 1)
        n += 1
    return text, n


def patch_file(path: Path) -> None:
    original = path.read_text(encoding="utf-8")
    text, wrap_count = patch_webpack_bootstrap(original)
    nil_count = 0
    if path.name.startswith("webapp"):
        text = capture_native_map(text)
        text, nil_count = patch_nil_externalize(text)
    if text == original:
        print(f"{path}: already patched")
        return
    path.write_text(text, encoding="utf-8")
    print(f"{path}: patched webpack bootstraps={wrap_count} nil-externalize={nil_count}")


def main(argv: list[str]) -> None:
    if len(argv) < 2:
        raise SystemExit("usage: preserve_native_map.py <file> [file...]")
    for raw in argv[1:]:
        patch_file(Path(raw))


if __name__ == "__main__":
    main(sys.argv)
