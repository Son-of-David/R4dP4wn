#!/usr/bin/env python3
# R4d-P4wn ST7789 control GUI
# Uses GUI/GUI.py as the display, input, font, and menu template.
# Mirrors as many web UI features as a 240x240 panel allows by driving
# P4wnP1_cli and the local HTTP APIs on 127.0.0.1:8000.
#
# Controls:
#   UP/DOWN   move
#   LEFT/RIGHT cycle options on the highlighted row
#   F2        select / toggle
#   ENTER     back (from the main menu: return to Status)
#   F1        refresh
#
# Optional: python3 gui.py /dev/input/eventX

import json
import os
import select
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime
from typing import Any, Callable, Dict, List, Optional, Sequence, Tuple

from evdev import InputDevice, list_devices, ecodes
from PIL import Image, ImageDraw, ImageFont
from st7789_userland import ST7789

DEVICE_PATH = sys.argv[1] if len(sys.argv) > 1 else None

WIDTH, HEIGHT = 240, 240
DISPLAY_CFG = dict(spi_speed=12_000_000, rotation=90, bgr=False, x_start=0, y_start=0)
LINE_HEIGHT = 18

API_BASE = os.environ.get("P4WNP1_API", "http://127.0.0.1:8000")
CLI_BIN = os.environ.get("P4WNP1_CLI") or shutil.which("P4wnP1_cli") or "/usr/local/bin/P4wnP1_cli"
P4WN_ROOT = "/usr/local/P4wnP1"
HID_DIR = os.path.join(P4WN_ROOT, "HIDScripts")
UMS_FLASH = os.path.join(P4WN_ROOT, "ums", "flashdrive")
UMS_CDROM = os.path.join(P4WN_ROOT, "ums", "cdrom")

COL_BG = (0, 0, 0)
COL_TITLE = (0, 220, 255)
COL_SEL = (255, 255, 255)
COL_ITEM = (200, 200, 200)
COL_MUTED = (160, 160, 160)
COL_OK = (80, 220, 120)
COL_WARN = (255, 200, 80)
COL_BAD = (255, 90, 90)
COL_BAR = (0, 255, 255)


def load_font(size: int):
    candidates = [
        "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf",
        "/usr/share/fonts/truetype/freefont/FreeSans.ttf",
    ]
    for path in candidates:
        try:
            return ImageFont.truetype(path, size)
        except Exception:
            pass
    return ImageFont.load_default()


FONT_TINY = load_font(13)
FONT_SMALL = load_font(16)
FONT_MED = load_font(20)


def find_gpio_keys() -> InputDevice:
    if DEVICE_PATH:
        return InputDevice(DEVICE_PATH)
    for path in list_devices():
        dev = InputDevice(path)
        name = (dev.name or "").lower()
        if "gpio-keys" in name or "gpio keys" in name:
            return dev
    expected = {ecodes.KEY_UP, ecodes.KEY_DOWN, ecodes.KEY_LEFT, ecodes.KEY_RIGHT,
                ecodes.KEY_ENTER, ecodes.KEY_F1, ecodes.KEY_F2, ecodes.KEY_F3}
    for path in list_devices():
        dev = InputDevice(path)
        caps = dev.capabilities().get(ecodes.EV_KEY, [])
        keys = {c[0] if isinstance(c, tuple) else c for c in caps}
        if len(expected & keys) >= 2:
            return dev
    raise RuntimeError("gpio-keys input device not found")


def read_loadavg_percent() -> str:
    try:
        load1, _, _ = os.getloadavg()
        cores = os.cpu_count() or 1
        pct = max(0.0, min(100.0, (load1 / cores) * 100.0))
        return f"{pct:.0f}%"
    except Exception:
        return "n/a"


def read_uptime() -> str:
    try:
        with open("/proc/uptime", "r") as f:
            seconds = float(f.read().split()[0])
        days = int(seconds // 86400)
        seconds %= 86400
        hours = int(seconds // 3600)
        seconds %= 3600
        minutes = int(seconds // 60)
        seconds = int(seconds % 60)
        if days:
            return f"{days}d {hours:02d}:{minutes:02d}"
        return f"{hours:02d}:{minutes:02d}:{seconds:02d}"
    except Exception:
        return "n/a"


def read_mem_usage() -> Tuple[str, str]:
    try:
        meminfo: Dict[str, str] = {}
        with open("/proc/meminfo", "r") as f:
            for line in f:
                k, v = line.split(":", 1)
                meminfo[k.strip()] = v.strip()

        def kB_to_bytes(kb_str: str) -> int:
            num = float(kb_str.split()[0])
            return int(num * 1024)

        total = kB_to_bytes(meminfo.get("MemTotal", "0 kB"))
        available = kB_to_bytes(meminfo.get("MemAvailable", "0 kB"))
        used = max(total - available, 0)
        pct = int(round(used * 100.0 / total)) if total else 0
        total_gib = total / (1024 ** 3)
        return str(pct), f"{total_gib:.1f} G"
    except Exception:
        return "n/a", "n/a"


def read_cpu_temp() -> str:
    candidates = [
        "/sys/class/thermal/thermal_zone0/temp",
        "/sys/devices/virtual/thermal/thermal_zone0/temp",
    ]
    for path in candidates:
        try:
            with open(path, "r") as f:
                raw = f.read().strip()
            val = float(raw)
            c = val / 1000.0 if val > 100.0 else val
            return f"{c:.1f}°C"
        except Exception:
            continue
    try:
        out = subprocess.check_output(["vcgencmd", "measure_temp"], timeout=1).decode()
        return out.strip().split("=")[-1]
    except Exception:
        return "n/a"


def clip(s: Any, n: int = 26) -> str:
    text = "" if s is None else str(s)
    text = text.replace("\n", " ").strip()
    if len(text) <= n:
        return text
    return text[: max(0, n - 1)] + "…"


def yn(v: Any) -> str:
    return "ON" if v else "off"


def first_err(*objs: Any) -> str:
    for obj in objs:
        if isinstance(obj, dict) and obj.get("error"):
            return str(obj["error"])
    return ""


def run_cmd(args: Sequence[str], timeout: float = 8) -> Tuple[int, str, str]:
    try:
        p = subprocess.run(list(args), capture_output=True, text=True, timeout=timeout)
        return p.returncode, (p.stdout or "").strip(), (p.stderr or "").strip()
    except Exception as e:
        return 1, "", str(e)


def cli_run(*args: str, timeout: float = 25) -> Tuple[int, str, str]:
    return run_cmd([CLI_BIN, *args], timeout=timeout)


def cli_json(*args: str, timeout: float = 25) -> Any:
    rc, out, err = cli_run(*args, timeout=timeout)
    if rc != 0:
        return {"error": err or out or f"cli failed ({rc})"}
    if not out:
        return {}
    try:
        return json.loads(out)
    except Exception:
        return {"error": out or err or "invalid JSON"}


def api_request(method: str, path: str, body: Optional[dict] = None, timeout: float = 12) -> dict:
    url = API_BASE + path
    data = None
    headers: Dict[str, str] = {}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode()
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        raw = e.read().decode(errors="replace")
        try:
            parsed = json.loads(raw) if raw else {}
            if isinstance(parsed, dict):
                parsed.setdefault("error", raw or str(e))
                return parsed
        except Exception:
            pass
        return {"error": raw or str(e)}
    except Exception as e:
        return {"error": str(e)}


def api_get(path: str, timeout: float = 8) -> dict:
    return api_request("GET", path, timeout=timeout)


def api_post(path: str, body: Optional[dict] = None, timeout: float = 20) -> dict:
    return api_request("POST", path, body or {}, timeout=timeout)


def iface_ipv4(name: str) -> str:
    path = f"/sys/class/net/{name}/operstate"
    try:
        with open(path, "r") as f:
            state = f.read().strip()
    except Exception:
        return ""
    rc, out, _ = run_cmd(["ip", "-4", "-o", "addr", "show", "dev", name], timeout=2)
    if rc != 0 or not out:
        return state
    parts = out.split()
    for i, p in enumerate(parts):
        if p == "inet" and i + 1 < len(parts):
            return parts[i + 1].split("/")[0]
    return state


def list_ifaces() -> List[str]:
    names = []
    try:
        for n in sorted(os.listdir("/sys/class/net")):
            if n != "lo":
                names.append(n)
    except Exception:
        pass
    return names


def udc_state() -> str:
    try:
        base = "/sys/class/udc"
        names = os.listdir(base)
        if not names:
            return "no UDC"
        with open(os.path.join(base, names[0], "state"), "r") as f:
            return f.read().strip()
    except Exception:
        return "n/a"


def parse_hostapd(path: str) -> Dict[str, str]:
    info: Dict[str, str] = {}
    try:
        with open(path, "r") as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                k, v = line.split("=", 1)
                info[k.strip()] = v.strip()
    except Exception:
        pass
    return info


def wifi_ap_line() -> str:
    conf = parse_hostapd("/tmp/hostapd_wlan0.conf")
    ssid = conf.get("ssid", "")
    ch = conf.get("channel", "")
    ip = iface_ipv4("wlan0")
    if ssid:
        extra = f" ch{ch}" if ch else ""
        return f"AP {ssid}{extra} {ip}".strip()
    if ip:
        return f"wlan0 {ip}"
    return "AP down"


def list_files(folder: str, suffixes: Sequence[str]) -> List[str]:
    names: List[str] = []
    try:
        for n in sorted(os.listdir(folder)):
            lower = n.lower()
            if any(lower.endswith(s) for s in suffixes):
                names.append(n)
    except Exception:
        pass
    return names


def parse_template_list(text: str) -> List[str]:
    names: List[str] = []
    for line in (text or "").splitlines():
        s = line.strip()
        if not s or s.startswith("-") or s.lower().startswith("templates of type") or s.lower().startswith("error"):
            continue
        names.append(s)
    return names


def list_templates(kind: str) -> List[str]:
    flag = {
        "usb": "--usb",
        "wifi": "--wifi",
        "bluetooth": "--bluetooth",
        "network": "--network",
        "trigger-actions": "--trigger-actions",
        "full": "--full",
    }[kind]
    rc, out, err = cli_run("template", "list", flag, timeout=12)
    if rc != 0:
        return []
    return parse_template_list(out or err)


def deploy_template(kind: str, name: str) -> str:
    flag = {
        "usb": "--usb",
        "wifi": "--wifi",
        "bluetooth": "--bluetooth",
        "network": "--network",
        "trigger-actions": "--trigger-actions",
        "full": "--full",
    }[kind]
    rc, out, err = cli_run("template", "deploy", flag, name, timeout=40)
    if rc != 0:
        return err or out or "deploy failed"
    return ""


def usb_settings(timeout: float = 10) -> dict:
    data = cli_json("usb", "get", "--json", timeout=timeout)
    return data if isinstance(data, dict) else {"error": "bad USB JSON"}


def usb_deploy(gs: dict) -> str:
    args = ["usb", "set"]
    if not gs.get("enabled"):
        args.append("--disable")
    if gs.get("use_RNDIS"):
        args.append("--rndis")
    if gs.get("use_CDC_ECM"):
        args.append("--cdc-ecm")
    if gs.get("use_SERIAL"):
        args.append("--serial")
    if gs.get("use_HID_KEYBOARD"):
        args.append("--hid-keyboard")
    if gs.get("use_HID_MOUSE"):
        args.append("--hid-mouse")
    if gs.get("use_HID_RAW"):
        args.append("--hid-raw")
    if gs.get("use_UMS"):
        args.append("--ums")
        ums = gs.get("ums_settings") or {}
        if ums.get("cdrom"):
            args.append("--ums-cdrom")
        if ums.get("file"):
            args.extend(["--ums-file", str(ums["file"])])
    if gs.get("vid"):
        args.extend(["--vid", str(gs["vid"])])
    if gs.get("pid"):
        args.extend(["--pid", str(gs["pid"])])
    if gs.get("manufacturer"):
        args.extend(["--manufacturer", str(gs["manufacturer"])])
    if gs.get("product"):
        args.extend(["--product", str(gs["product"])])
    if gs.get("serial"):
        args.extend(["--sn", str(gs["serial"])])
    rc, out, err = cli_run(*args, timeout=45)
    if rc != 0:
        return err or out or "USB deploy failed"
    return ""


def usb_functions_short(gs: dict) -> str:
    flags = []
    for key, letter in (
        ("use_RNDIS", "R"),
        ("use_CDC_ECM", "E"),
        ("use_SERIAL", "S"),
        ("use_HID_KEYBOARD", "K"),
        ("use_HID_MOUSE", "M"),
        ("use_HID_RAW", "G"),
        ("use_UMS", "U"),
    ):
        if gs.get(key):
            flags.append(letter)
    return "".join(flags) or "-"


def led_count() -> str:
    rc, out, err = cli_run("led", timeout=6)
    text = out or err
    for tok in text.replace("=", " ").split():
        if tok.isdigit():
            return tok
    return text or "n/a"


def wait_keys(dev: InputDevice, timeout: float) -> List[int]:
    r, _, _ = select.select([dev.fd], [], [], timeout)
    if not r:
        return []
    codes: List[int] = []
    try:
        for event in dev.read():
            if event.type == ecodes.EV_KEY and event.value == 1:
                codes.append(event.code)
    except BlockingIOError:
        pass
    return codes


def new_image() -> Tuple[Image.Image, ImageDraw.ImageDraw]:
    img = Image.new("RGB", (WIDTH, HEIGHT), COL_BG)
    return img, ImageDraw.Draw(img)


def show_message(disp: ST7789, lines: List[str], hold_sec: float = 0.0):
    img, d = new_image()
    y = 28
    for ln in lines:
        d.text((WIDTH // 2, y), clip(ln, 28), font=FONT_SMALL, fill=COL_SEL, anchor="mm")
        y += 22
    disp.display(img)
    if hold_sec > 0:
        time.sleep(hold_sec)


def confirm(disp: ST7789, dev: InputDevice, lines: List[str]) -> bool:
    img, d = new_image()
    y = 24
    for ln in lines:
        d.text((WIDTH // 2, y), clip(ln, 28), font=FONT_SMALL, fill=COL_SEL, anchor="mm")
        y += 22
    d.text((WIDTH // 2, HEIGHT - 18), "F2=Yes  ENTER=No", font=FONT_TINY, fill=COL_MUTED, anchor="mm")
    disp.display(img)
    while True:
        for code in wait_keys(dev, 0.4):
            if code == ecodes.KEY_F2:
                return True
            if code in (ecodes.KEY_ENTER, ecodes.KEY_ESC):
                return False


class Menu:
    def __init__(self, title: str, items: List[Tuple[str, str]]):
        self.title = title
        self.items = items
        self.index = 0

    def move(self, delta: int):
        if not self.items:
            self.index = 0
            return
        self.index = (self.index + delta) % len(self.items)

    def current(self) -> Tuple[str, str]:
        if not self.items:
            return "", ""
        return self.items[self.index]

    def set_items(self, items: List[Tuple[str, str]]):
        self.items = items
        if self.items:
            self.index %= len(self.items)
        else:
            self.index = 0


def draw_status_screen(disp: ST7789, extra: Optional[Dict[str, str]] = None):
    extra = extra or {}
    img, d = new_image()
    y = 8
    d.text((WIDTH // 2, y), "R4d-P4wn", font=FONT_MED, fill=COL_TITLE, anchor="mm")
    y += 22
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    d.text((8, y), clip(f"Time: {now}", 28), font=FONT_TINY, fill=(200, 255, 255))
    y += 16
    mem_pct, mem_total = read_mem_usage()
    d.text((8, y), clip(f"Load {read_loadavg_percent()}  Mem {mem_pct}%/{mem_total}", 28), font=FONT_TINY, fill=COL_SEL)
    y += 16
    d.text((8, y), clip(f"CPU {read_cpu_temp()}  Up {read_uptime()}", 28), font=FONT_TINY, fill=COL_SEL)
    y += 18
    for key in ("usb", "wifi", "wlan1", "share", "vpn"):
        val = extra.get(key)
        if not val:
            continue
        color = COL_OK if extra.get(f"{key}_ok") else COL_ITEM
        d.text((8, y), clip(val, 28), font=FONT_TINY, fill=color)
        y += 16
    d.text((WIDTH // 2, HEIGHT - 16), "Press any key for Menu", font=FONT_TINY, fill=COL_MUTED, anchor="mm")
    disp.display(img)


def status_snapshot() -> Dict[str, str]:
    gs = usb_settings(timeout=2)
    sta = api_get("/api/wifi/status", timeout=1.5)
    share = api_get("/api/share/status", timeout=1.5)
    usb_on = bool(gs.get("enabled")) if not gs.get("error") else False
    usb = f"USB {yn(usb_on)} {usb_functions_short(gs)} {udc_state()}"
    wifi = wifi_ap_line()
    wlan1 = "wlan1 " + (sta.get("ip") or sta.get("ssid") or ("up" if sta.get("running") else "down"))
    if sta.get("error") and not sta.get("running"):
        wlan1 = "wlan1 n/a"
    sharing = bool(share.get("sharing"))
    share_line = "Share " + ("VPN" if share.get("via_vpn") else "WiFi") if sharing else "Share off"
    if share.get("uplink"):
        share_line += f" via {share.get('uplink')}"
    vpn = "VPN " + (share.get("vpn_file") or share.get("vpn_kind") or "up") if share.get("vpn_running") else "VPN off"
    return {
        "usb": usb,
        "usb_ok": usb_on,
        "wifi": wifi,
        "wifi_ok": wifi.startswith("AP ") and "down" not in wifi,
        "wlan1": wlan1,
        "wlan1_ok": bool(sta.get("running")),
        "share": share_line,
        "share_ok": sharing,
        "vpn": vpn,
        "vpn_ok": bool(share.get("vpn_running")),
    }


def draw_menu(disp: ST7789, menu: Menu, status_lines: Optional[List[str]] = None,
              footer: str = "ENTER=Back  F2=Select", hint: str = ""):
    img, d = new_image()
    d.text((WIDTH // 2, 12), clip(menu.title, 22), font=FONT_MED, fill=COL_TITLE, anchor="mm")
    y = 28
    if status_lines:
        for ln in status_lines[:3]:
            d.text((8, y), clip(ln, 28), font=FONT_TINY, fill=COL_MUTED)
            y += 14
    if hint:
        d.text((8, y), clip(hint, 28), font=FONT_TINY, fill=COL_WARN)
        y += 14
    y += 4
    footer_y = HEIGHT - 16
    avail = footer_y - 18 - y
    row_h = 20
    visible = max(1, avail // row_h)
    n = len(menu.items)
    start = 0
    if n > visible:
        start = min(max(0, menu.index - visible // 2), n - visible)
    for i in range(start, min(n, start + visible)):
        label, _ = menu.items[i]
        row_y = y + (i - start) * row_h
        if i == menu.index:
            d.rectangle((6, row_y - 1, WIDTH - 6, row_y + row_h - 3), outline=COL_BAR, width=1)
            fill = COL_SEL
        else:
            fill = COL_ITEM
        d.text((12, row_y + 1), clip(label, 26), font=FONT_SMALL, fill=fill)
    d.text((WIDTH // 2, footer_y), clip(footer, 32), font=FONT_TINY, fill=COL_MUTED, anchor="mm")
    disp.display(img)


def run_menu(disp: ST7789, dev: InputDevice, menu: Menu,
             refresh: Optional[Callable[[], None]] = None,
             on_select: Optional[Callable[[str], Optional[str]]] = None,
             on_cycle: Optional[Callable[[str, int], None]] = None,
             status_fn: Optional[Callable[[], List[str]]] = None,
             footer: str = "ENTER=Back  F2=Select",
             timeout: float = 0.8,
             auto_refresh: float = 0.0,
             exit_on_select: bool = False) -> None:
    hint = ""
    last = 0.0
    while True:
        now = time.time()
        if refresh and (last == 0.0 or (auto_refresh and now - last >= auto_refresh)):
            refresh()
            last = now
        status_lines = status_fn() if status_fn else None
        draw_menu(disp, menu, status_lines=status_lines, footer=footer, hint=hint)
        hint = ""
        for code in wait_keys(dev, timeout):
            if code == ecodes.KEY_UP:
                menu.move(-1)
            elif code == ecodes.KEY_DOWN:
                menu.move(1)
            elif code == ecodes.KEY_LEFT and on_cycle:
                _, action = menu.current()
                on_cycle(action, -1)
            elif code == ecodes.KEY_RIGHT and on_cycle:
                _, action = menu.current()
                on_cycle(action, 1)
            elif code == ecodes.KEY_F1:
                if refresh:
                    refresh()
                    last = time.time()
            elif code == ecodes.KEY_F2:
                _, action = menu.current()
                if not action:
                    continue
                if on_select:
                    msg = on_select(action)
                    if msg:
                        hint = msg
                if exit_on_select:
                    return
            elif code in (ecodes.KEY_ENTER, ecodes.KEY_ESC):
                return


def pick_list(disp: ST7789, dev: InputDevice, title: str, names: List[str],
              empty: str = "Nothing stored") -> Optional[str]:
    if not names:
        show_message(disp, [title, empty], hold_sec=1.2)
        return None
    menu = Menu(title, [(n, n) for n in names])
    chosen: List[str] = []

    def on_select(action: str) -> Optional[str]:
        chosen.append(action)
        return "ok"

    run_menu(disp, dev, menu, on_select=on_select, timeout=0.5, exit_on_select=True)
    return chosen[-1] if chosen else None


def show_lines(disp: ST7789, dev: InputDevice, title: str, lines: List[str]):
    offset = 0
    while True:
        img, d = new_image()
        d.text((WIDTH // 2, 12), clip(title, 22), font=FONT_MED, fill=COL_TITLE, anchor="mm")
        y = 30
        view = lines[offset:offset + 10] or ["(empty)"]
        for ln in view:
            d.text((8, y), clip(ln, 28), font=FONT_TINY, fill=COL_SEL)
            y += 16
        d.text((WIDTH // 2, HEIGHT - 16), "UP/DOWN scroll  ENTER back", font=FONT_TINY, fill=COL_MUTED, anchor="mm")
        disp.display(img)
        for code in wait_keys(dev, 0.5):
            if code == ecodes.KEY_UP:
                offset = max(0, offset - 1)
            elif code == ecodes.KEY_DOWN:
                offset = min(max(0, len(lines) - 1), offset + 1)
            elif code in (ecodes.KEY_ENTER, ecodes.KEY_ESC, ecodes.KEY_F2):
                return


def result_msg(disp: ST7789, err: str, ok: str = "Done"):
    if err:
        show_message(disp, ["Error", err], hold_sec=1.4)
    else:
        show_message(disp, [ok], hold_sec=0.7)


def usb_screen(disp: ST7789, dev: InputDevice):
    gs: Dict[str, Any] = {}

    def refresh():
        nonlocal gs
        gs = usb_settings()

    def items() -> List[Tuple[str, str]]:
        ums = gs.get("ums_settings") or {}
        mode = "CD" if ums.get("cdrom") else "flash"
        fname = os.path.basename(str(ums.get("file") or "")) or "-"
        return [
            (f"Gadget {yn(gs.get('enabled'))}", "enabled"),
            (f"RNDIS {yn(gs.get('use_RNDIS'))}", "use_RNDIS"),
            (f"CDC ECM {yn(gs.get('use_CDC_ECM'))}", "use_CDC_ECM"),
            (f"Serial {yn(gs.get('use_SERIAL'))}", "use_SERIAL"),
            (f"HID Keyboard {yn(gs.get('use_HID_KEYBOARD'))}", "use_HID_KEYBOARD"),
            (f"HID Mouse {yn(gs.get('use_HID_MOUSE'))}", "use_HID_MOUSE"),
            (f"HID Raw {yn(gs.get('use_HID_RAW'))}", "use_HID_RAW"),
            (f"UMS {yn(gs.get('use_UMS'))}", "use_UMS"),
            (f"UMS mode {mode}", "ums_mode"),
            (f"UMS file {fname}", "ums_file"),
            ("USB templates…", "templates"),
        ]

    menu = Menu("USB", items())

    def rebuild():
        keep = menu.index
        menu.set_items(items())
        menu.index = min(keep, max(0, len(menu.items) - 1))

    def status_fn() -> List[str]:
        if gs.get("error"):
            return [str(gs["error"])]
        return [
            f"{gs.get('product') or 'gadget'} {udc_state()}",
            f"{gs.get('vid') or ''} {gs.get('pid') or ''} {gs.get('serial') or ''}".strip(),
        ]

    def apply():
        show_message(disp, ["Deploying USB…"])
        err = usb_deploy(gs)
        result_msg(disp, err, "USB updated")
        refresh()
        rebuild()

    def on_select(action: str) -> Optional[str]:
        if action == "templates":
            name = pick_list(disp, dev, "USB templates", list_templates("usb"))
            if name and confirm(disp, dev, ["Deploy USB template?", name]):
                show_message(disp, ["Deploying…", name])
                result_msg(disp, deploy_template("usb", name), "Template deployed")
                refresh()
                rebuild()
            return None
        if action == "ums_file":
            cdrom = bool((gs.get("ums_settings") or {}).get("cdrom"))
            folder = UMS_CDROM if cdrom else UMS_FLASH
            names = list_files(folder, (".img", ".iso", ".bin", ".dd"))
            name = pick_list(disp, dev, "UMS images", names, empty=f"No images in {folder}")
            if not name:
                return None
            path = os.path.join(folder, name)
            show_message(disp, ["Mounting UMS…", name])
            args = ["usb", "mount", "--ums-file", path]
            if cdrom:
                args.append("--ums-cdrom")
            rc, out, err = cli_run(*args, timeout=20)
            result_msg(disp, (err or out) if rc else "", "UMS mounted")
            refresh()
            rebuild()
            return None
        if action == "ums_mode":
            ums = dict(gs.get("ums_settings") or {})
            ums["cdrom"] = not ums.get("cdrom")
            gs["ums_settings"] = ums
            gs["use_UMS"] = True
            apply()
            return None
        gs[action] = not bool(gs.get(action))
        apply()
        return None

    def on_cycle(action: str, delta: int):
        if action == "ums_mode":
            on_select(action)

    refresh()
    rebuild()
    run_menu(disp, dev, menu, refresh=lambda: (refresh(), rebuild()),
             on_select=on_select, on_cycle=on_cycle, status_fn=status_fn,
             auto_refresh=0)


def wifi_screen(disp: ST7789, dev: InputDevice):
    sta: dict = {}

    def refresh():
        nonlocal sta
        sta = api_get("/api/wifi/status")

    def items() -> List[Tuple[str, str]]:
        return [
            ("Disconnect client", "disconnect"),
            ("WiFi templates…", "templates"),
            ("Refresh status", "refresh"),
        ]

    menu = Menu("WiFi", items())

    def status_fn() -> List[str]:
        lines = [wifi_ap_line()]
        if sta.get("error"):
            lines.append(str(sta["error"]))
        else:
            client = "down"
            if sta.get("running"):
                client = f"{sta.get('iface') or 'sta'} {sta.get('ssid') or ''} {sta.get('ip') or ''}".strip()
            lines.append("Client " + client)
            if sta.get("note"):
                lines.append(str(sta["note"]))
        return lines

    def on_select(action: str) -> Optional[str]:
        if action == "refresh":
            refresh()
            return None
        if action == "disconnect":
            iface = sta.get("iface") or "wlan1"
            if not confirm(disp, dev, ["Disconnect WiFi client?", iface]):
                return None
            show_message(disp, ["Disconnecting…"])
            res = api_post("/api/wifi/disconnect", {"iface": iface})
            result_msg(disp, first_err(res), "Disconnected")
            refresh()
            return None
        if action == "templates":
            name = pick_list(disp, dev, "WiFi templates", list_templates("wifi"))
            if name and confirm(disp, dev, ["Deploy WiFi template?", name]):
                show_message(disp, ["Deploying…", name])
                result_msg(disp, deploy_template("wifi", name), "Template deployed")
                refresh()
            return None
        return None

    run_menu(disp, dev, menu, refresh=refresh, on_select=on_select, status_fn=status_fn,
             auto_refresh=2.0)


def share_screen(disp: ST7789, dev: InputDevice):
    st: dict = {}
    kind = "wireguard"
    files: List[str] = []
    selected = 0
    dns_modes = ["default", "off"]
    seeded = False

    def refresh():
        nonlocal st, files, selected, kind, seeded
        st = api_get("/api/share/status")
        listed = api_get("/api/share/vpn/list?" + urllib.parse.urlencode({"kind": kind}))
        files = list(listed.get("files") or [])
        if not seeded:
            if st.get("boot_vpn_kind") in ("wireguard", "openvpn"):
                kind = st["boot_vpn_kind"]
                listed = api_get("/api/share/vpn/list?" + urllib.parse.urlencode({"kind": kind}))
                files = list(listed.get("files") or [])
            if st.get("vpn_file") in files:
                selected = files.index(st["vpn_file"])
            elif st.get("boot_vpn_name") in files:
                selected = files.index(st["boot_vpn_name"])
            seeded = True
        if files:
            selected %= len(files)
        else:
            selected = 0

    def current_file() -> str:
        return files[selected] if files else ""

    def items() -> List[Tuple[str, str]]:
        dns = st.get("dns_mode") or "default"
        return [
            ("Share WiFi to usbeth", "share_wifi"),
            ("Share VPN to usbeth", "share_vpn"),
            ("Stop sharing", "share_stop"),
            (f"VPN type {kind}", "vpn_kind"),
            (f"Profile {current_file() or '(none)'}", "vpn_file"),
            ("Connect VPN", "vpn_start"),
            ("Disconnect VPN", "vpn_stop"),
            (f"Boot share {yn(st.get('boot_share'))}", "boot_share"),
            (f"Boot VPN {yn(st.get('boot_vpn'))}", "boot_vpn"),
            (f"DNS {dns}", "dns"),
        ]

    menu = Menu("Share / VPN", items())

    def rebuild():
        keep = menu.index
        menu.set_items(items())
        menu.index = min(keep, max(0, len(menu.items) - 1))

    def status_fn() -> List[str]:
        if st.get("error"):
            return [str(st["error"])]
        share = "off"
        if st.get("sharing"):
            share = "VPN" if st.get("via_vpn") else "WiFi"
            if st.get("uplink"):
                share += f" {st.get('uplink')}"
        vpn = st.get("vpn_file") or st.get("vpn_kind") or "off"
        if not st.get("vpn_running"):
            vpn = "off"
        return [
            f"Share {share}  usbeth {st.get('usbeth_ip') or '-'}",
            f"wlan0 {st.get('wlan0_ip') or '-'}  wlan1 {st.get('wlan1_ip') or '-'}",
            f"VPN {vpn} {st.get('vpn_ip') or ''}".strip(),
        ]

    def save_boot(**overrides):
        name = current_file() or st.get("boot_vpn_name") or ""
        body = {
            "vpn_enable": bool(overrides.get("vpn_enable", st.get("boot_vpn"))),
            "vpn_kind": kind,
            "vpn_name": name,
            "share_enable": bool(overrides.get("share_enable", st.get("boot_share"))),
            "share_via_vpn": bool(overrides.get("vpn_enable", st.get("boot_vpn"))),
            "dns_mode": overrides.get("dns_mode", st.get("dns_mode") or "default"),
            "dns": st.get("dns") or "8.8.8.8,1.1.1.1",
        }
        return api_post("/api/share/boot", body)

    def on_select(action: str) -> Optional[str]:
        nonlocal kind, selected
        if action == "share_wifi":
            show_message(disp, ["Starting share…"])
            res = api_post("/api/share/start", {"via_vpn": False})
            result_msg(disp, first_err(res), "Share started")
        elif action == "share_vpn":
            show_message(disp, ["Starting VPN share…"])
            res = api_post("/api/share/start", {"via_vpn": True})
            result_msg(disp, first_err(res), "VPN share started")
        elif action == "share_stop":
            show_message(disp, ["Stopping share…"])
            res = api_post("/api/share/stop", {})
            result_msg(disp, first_err(res), "Share stopped")
        elif action == "vpn_kind":
            kind = "openvpn" if kind == "wireguard" else "wireguard"
            selected = 0
        elif action == "vpn_file":
            if files:
                selected = (selected + 1) % len(files)
        elif action == "vpn_start":
            name = current_file()
            if not name:
                return "Save a VPN profile in the web UI"
            if not confirm(disp, dev, ["Connect VPN?", name]):
                return None
            show_message(disp, ["Connecting VPN…", name])
            res = api_post("/api/share/vpn/start", {"kind": kind, "name": name})
            result_msg(disp, first_err(res), "VPN connecting")
        elif action == "vpn_stop":
            show_message(disp, ["Disconnecting VPN…"])
            res = api_post("/api/share/vpn/stop", {})
            result_msg(disp, first_err(res), "VPN stopped")
        elif action == "boot_share":
            res = save_boot(share_enable=not st.get("boot_share"))
            result_msg(disp, first_err(res), "Boot share saved")
        elif action == "boot_vpn":
            if not current_file() and not st.get("boot_vpn"):
                return "Pick a VPN profile first"
            res = save_boot(vpn_enable=not st.get("boot_vpn"))
            result_msg(disp, first_err(res), "Boot VPN saved")
        elif action == "dns":
            cur = st.get("dns_mode") or "default"
            nxt = dns_modes[(dns_modes.index(cur) + 1) % len(dns_modes)] if cur in dns_modes else "default"
            res = save_boot(dns_mode=nxt)
            result_msg(disp, first_err(res), f"DNS {nxt}")
        refresh()
        rebuild()
        return None

    def on_cycle(action: str, delta: int):
        nonlocal kind, selected
        if action == "vpn_kind":
            kind = "openvpn" if kind == "wireguard" else "wireguard"
            selected = 0
            refresh()
            rebuild()
        elif action == "vpn_file" and files:
            selected = (selected + delta) % len(files)
            rebuild()
        elif action == "dns":
            on_select("dns")

    refresh()
    rebuild()
    run_menu(disp, dev, menu, refresh=lambda: (refresh(), rebuild()),
             on_select=on_select, on_cycle=on_cycle, status_fn=status_fn,
             footer="L/R cycle  F2 select", auto_refresh=2.0)


def hid_screen(disp: ST7789, dev: InputDevice):
    scripts = list_files(HID_DIR, (".js", ".javascript"))
    if not scripts:
        show_message(disp, ["HID Scripts", "None in HIDScripts/"], hold_sec=1.4)
        return
    menu = Menu("HID Scripts", [(n, n) for n in scripts])

    def status_fn() -> List[str]:
        gs = usb_settings()
        kbd = yn(gs.get("use_HID_KEYBOARD"))
        mouse = yn(gs.get("use_HID_MOUSE"))
        return [f"Stored scripts in HIDScripts/", f"Keyboard {kbd}  Mouse {mouse}"]

    def on_select(name: str) -> Optional[str]:
        if not confirm(disp, dev, ["Run stored HID script?", name, "as a background job"]):
            return None
        show_message(disp, ["Starting job…", name])
        rc, out, err = cli_run("hid", "job", "--name", name, timeout=15)
        if rc != 0:
            result_msg(disp, err or out or "HID job failed")
        else:
            result_msg(disp, "", out or "Job started")
        return None

    run_menu(disp, dev, menu, on_select=on_select, status_fn=status_fn)


def templates_screen(disp: ST7789, dev: InputDevice):
    kinds = [
        ("USB templates", "usb"),
        ("WiFi templates", "wifi"),
        ("Bluetooth templates", "bluetooth"),
        ("Network templates", "network"),
        ("Trigger templates", "trigger-actions"),
        ("Full / master templates", "full"),
    ]
    menu = Menu("Templates", kinds)

    def on_select(kind: str) -> Optional[str]:
        name = pick_list(disp, dev, kind, list_templates(kind))
        if name and confirm(disp, dev, ["Deploy template?", name]):
            show_message(disp, ["Deploying…", name])
            result_msg(disp, deploy_template(kind, name), "Deployed")
        return None

    run_menu(disp, dev, menu, on_select=on_select)


def capture_screen(disp: ST7789, dev: InputDevice):
    st: dict = {}
    tool = "tshark"
    iface = "usbeth"
    ifaces: List[str] = []

    def refresh():
        nonlocal st, ifaces, iface, tool
        st = api_get("/api/capture/status")
        ifaces = [i.get("name") for i in (st.get("ifaces") or []) if i.get("name")]
        if not ifaces:
            ifaces = [n for n in ("usbeth", "wlan0", "wlan1", "any") if n in list_ifaces() or n == "any"]
        if iface not in ifaces and ifaces:
            iface = ifaces[0]
        if st.get("tool") in ("tshark", "tcpdump") and st.get("running"):
            tool = st["tool"]
        if not st.get("tshark_bin") and st.get("tcpdump_bin"):
            tool = "tcpdump"

    def items() -> List[Tuple[str, str]]:
        return [
            (f"Tool {tool}", "tool"),
            (f"Iface {iface}", "iface"),
            ("Start capture", "start"),
            ("Stop and save", "stop"),
            ("Saved files…", "files"),
        ]

    menu = Menu("Capture", items())

    def rebuild():
        keep = menu.index
        menu.set_items(items())
        menu.index = min(keep, max(0, len(menu.items) - 1))

    def status_fn() -> List[str]:
        if st.get("error"):
            return [str(st["error"])]
        if st.get("running"):
            return [
                f"Running {st.get('tool')} {st.get('iface')}",
                f"{st.get('pcap') or ''} {st.get('started_ago') or ''}".strip(),
            ]
        note = st.get("note") or "Idle"
        return [note, st.get("dir") or ""]

    def on_select(action: str) -> Optional[str]:
        nonlocal tool, iface
        if action == "tool":
            tool = "tcpdump" if tool == "tshark" else "tshark"
            rebuild()
            return None
        if action == "iface":
            if ifaces:
                iface = ifaces[(ifaces.index(iface) + 1) % len(ifaces)] if iface in ifaces else ifaces[0]
            rebuild()
            return None
        if action == "start":
            if not confirm(disp, dev, ["Start packet capture?", f"{tool} on {iface}"]):
                return None
            show_message(disp, ["Starting capture…"])
            res = api_post("/api/capture/start", {"tool": tool, "iface": iface, "promisc": True})
            result_msg(disp, first_err(res), "Capture started")
        elif action == "stop":
            show_message(disp, ["Stopping capture…"])
            res = api_post("/api/capture/stop", {})
            result_msg(disp, first_err(res), "Capture saved")
        elif action == "files":
            body = api_get("/api/capture/files")
            names = [f.get("name") for f in (body.get("files") or []) if f.get("name")]
            name = pick_list(disp, dev, "Pcap files", names, empty="No saved pcaps")
            if name and confirm(disp, dev, ["Delete capture file?", name]):
                res = api_post("/api/capture/delete", {"name": name})
                result_msg(disp, first_err(res), "Deleted")
        refresh()
        rebuild()
        return None

    def on_cycle(action: str, delta: int):
        nonlocal tool, iface
        if action == "tool":
            tool = "tcpdump" if tool == "tshark" else "tshark"
            rebuild()
        elif action == "iface" and ifaces:
            idx = ifaces.index(iface) if iface in ifaces else 0
            iface = ifaces[(idx + delta) % len(ifaces)]
            rebuild()

    refresh()
    rebuild()
    run_menu(disp, dev, menu, refresh=lambda: (refresh(), rebuild()),
             on_select=on_select, on_cycle=on_cycle, status_fn=status_fn,
             footer="L/R cycle  F2 select", auto_refresh=2.0)


def kismet_screen(disp: ST7789, dev: InputDevice):
    st: dict = {}

    def refresh():
        nonlocal st
        st = api_get("/api/kismet/status")

    menu = Menu("Kismet", [
        ("Start on wlan0", "wlan0"),
        ("Start on wlan1", "wlan1"),
        ("Stop Kismet", "stop"),
    ])

    def status_fn() -> List[str]:
        if st.get("error"):
            return [str(st["error"])]
        run = "running" if st.get("running") else "stopped"
        return [
            f"{run} {st.get('iface') or ''} port {st.get('ui_port') or 2501}",
            f"USB WiFi {', '.join(st.get('usb_wifi') or []) or 'none'}",
        ]

    def on_select(action: str) -> Optional[str]:
        if action in ("wlan0", "wlan1"):
            warn = ["Start Kismet?", action]
            if action == "wlan0":
                warn.append("Stops onboard AP")
            if not confirm(disp, dev, warn):
                return None
            show_message(disp, ["Starting Kismet…", action])
            res = api_post("/api/kismet/start", {"source": action})
            result_msg(disp, first_err(res), "Kismet started")
        elif action == "stop":
            show_message(disp, ["Stopping Kismet…"])
            res = api_post("/api/kismet/stop", {})
            result_msg(disp, first_err(res), "Kismet stopped")
        refresh()
        return None

    run_menu(disp, dev, menu, refresh=refresh, on_select=on_select, status_fn=status_fn,
             auto_refresh=2.0)


def gps_screen(disp: ST7789, dev: InputDevice):
    st: dict = {}

    def refresh():
        nonlocal st
        st = api_get("/api/gps/status")

    menu = Menu("GPS", [
        ("Start GPS", "start"),
        ("Stop GPS", "stop"),
        ("View output", "view"),
    ])

    def status_fn() -> List[str]:
        run = "running" if st.get("running") else "stopped"
        view = "view on" if st.get("viewing") else "view off"
        return [
            f"{run}  {view}",
            st.get("pipe_note") or f"{st.get('pipe_host') or ''}:{st.get('pipe_port') or ''}",
        ]

    def on_select(action: str) -> Optional[str]:
        if action == "start":
            show_message(disp, ["Starting GPS…"])
            res = api_post("/api/gps/start", {})
            result_msg(disp, first_err(res), "GPS started")
        elif action == "stop":
            show_message(disp, ["Stopping GPS…"])
            res = api_post("/api/gps/stop", {})
            result_msg(disp, first_err(res), "GPS stopped")
        elif action == "view":
            refresh()
            out = (st.get("output") or "No GPS output yet").splitlines()
            show_lines(disp, dev, "GPS output", out)
        refresh()
        return None

    run_menu(disp, dev, menu, refresh=refresh, on_select=on_select, status_fn=status_fn,
             auto_refresh=2.0)


def airgeddon_screen(disp: ST7789, dev: InputDevice):
    st: dict = {}

    def refresh():
        nonlocal st
        st = api_get("/api/airgeddon/status")

    menu = Menu("Airgeddon", [
        ("Start session", "start"),
        ("Stop session", "stop"),
        ("wlan0 monitor mode", "monitor"),
        ("Restore wlan0 AP", "restore"),
    ])

    def status_fn() -> List[str]:
        run = "running" if st.get("running") else "stopped"
        return [
            f"{run} {st.get('session') or ''}",
            f"mon {st.get('monitor') or '-'}  {st.get('airgeddon_bin') or ''}",
        ]

    def on_select(action: str) -> Optional[str]:
        if action == "start":
            if not confirm(disp, dev, ["Start airgeddon session?", "Use the web UI for the TTY"]):
                return None
            show_message(disp, ["Starting airgeddon…"])
            res = api_post("/api/airgeddon/start", {})
            result_msg(disp, first_err(res), "Started")
        elif action == "stop":
            show_message(disp, ["Stopping airgeddon…"])
            res = api_post("/api/airgeddon/stop", {})
            result_msg(disp, first_err(res), "Stopped")
        elif action == "monitor":
            if not confirm(disp, dev, ["Put wlan0 in monitor?", "Onboard AP will stop"]):
                return None
            show_message(disp, ["Setting monitor…"])
            res = api_post("/api/airgeddon/monitor", {})
            result_msg(disp, first_err(res), res.get("monitor") or "Monitor ready")
        elif action == "restore":
            if not confirm(disp, dev, ["Restore wlan0 AP?"]):
                return None
            show_message(disp, ["Restoring AP…"])
            res = api_post("/api/airgeddon/restore", {})
            result_msg(disp, first_err(res), "Restore requested")
        refresh()
        return None

    run_menu(disp, dev, menu, refresh=refresh, on_select=on_select, status_fn=status_fn,
             auto_refresh=2.0)


def bluetooth_screen(disp: ST7789, dev: InputDevice):
    def bt_lines() -> List[str]:
        lines = []
        rc, out, _ = run_cmd(["hciconfig", "-a"], timeout=3)
        if rc == 0 and out:
            for raw in out.splitlines():
                s = raw.strip()
                if s.startswith("hci") or s.startswith("Name:") or s.startswith("UP ") or s.startswith("DOWN") or "PSCAN" in s or s.startswith("Manufacturer:"):
                    lines.append(s)
        else:
            rc, out, _ = run_cmd(["bluetoothctl", "show"], timeout=3)
            for raw in (out or "").splitlines():
                if any(k in raw for k in ("Name:", "Powered:", "Discoverable:", "Alias:")):
                    lines.append(raw.strip())
        return lines or ["Bluetooth status unavailable"]

    menu = Menu("Bluetooth", [
        ("View adapter", "view"),
        ("Bluetooth templates…", "templates"),
    ])

    def on_select(action: str) -> Optional[str]:
        if action == "view":
            show_lines(disp, dev, "Bluetooth", bt_lines())
        elif action == "templates":
            name = pick_list(disp, dev, "BT templates", list_templates("bluetooth"))
            if name and confirm(disp, dev, ["Deploy BT template?", name]):
                show_message(disp, ["Deploying…", name])
                result_msg(disp, deploy_template("bluetooth", name), "Deployed")
        return None

    run_menu(disp, dev, menu, on_select=on_select, status_fn=lambda: bt_lines()[:3])


def network_screen(disp: ST7789, dev: InputDevice):
    def net_lines() -> List[str]:
        lines = []
        for name in list_ifaces():
            ip = iface_ipv4(name)
            if ip:
                lines.append(f"{name} {ip}")
        return lines or ["No interfaces"]

    menu = Menu("Network", [
        ("Interface addresses", "view"),
        ("Network templates…", "templates"),
    ])

    def on_select(action: str) -> Optional[str]:
        if action == "view":
            show_lines(disp, dev, "Addresses", net_lines())
        elif action == "templates":
            name = pick_list(disp, dev, "Net templates", list_templates("network"))
            if name and confirm(disp, dev, ["Deploy net template?", name]):
                show_message(disp, ["Deploying…", name])
                result_msg(disp, deploy_template("network", name), "Deployed")
        return None

    run_menu(disp, dev, menu, on_select=on_select, status_fn=lambda: net_lines()[:3])


def trigger_screen(disp: ST7789, dev: InputDevice):
    menu = Menu("Triggers", [
        ("Trigger templates…", "templates"),
    ])

    def on_select(action: str) -> Optional[str]:
        name = pick_list(disp, dev, "Trigger templates", list_templates("trigger-actions"))
        if name and confirm(disp, dev, ["Deploy triggers?", name]):
            show_message(disp, ["Deploying…", name])
            result_msg(disp, deploy_template("trigger-actions", name), "Deployed")
        return None

    run_menu(disp, dev, menu, on_select=on_select,
             status_fn=lambda: ["Deploy stored TriggerAction", "templates from the web UI"])


def log_screen(disp: ST7789, dev: InputDevice):
    rc, out, err = run_cmd(
        ["journalctl", "-u", "P4wnP1", "-n", "24", "--no-pager", "-o", "short-iso"],
        timeout=6,
    )
    text = out or err or "No journal output"
    show_lines(disp, dev, "Event Log", text.splitlines())


def generic_screen(disp: ST7789, dev: InputDevice):
    menu = Menu("Generic", [
        ("LED off", "led0"),
        ("LED blink 1", "led1"),
        ("LED blink 5", "led5"),
        ("LED solid", "led255"),
        ("Master templates…", "full"),
        ("DB backups…", "db"),
        ("Reboot", "reboot"),
        ("Shutdown", "shutdown"),
    ])

    def status_fn() -> List[str]:
        return [f"LED blink {led_count()}", "Startup templates via --full"]

    def on_select(action: str) -> Optional[str]:
        if action.startswith("led"):
            n = action.replace("led", "")
            rc, out, err = cli_run("led", "--blink", n, timeout=6)
            result_msg(disp, (err or out) if rc else "", f"LED {n}")
            return None
        if action == "full":
            name = pick_list(disp, dev, "Master templates", list_templates("full"))
            if name and confirm(disp, dev, ["Deploy master template?", name]):
                show_message(disp, ["Deploying…", name])
                result_msg(disp, deploy_template("full", name), "Deployed")
            return None
        if action == "db":
            rc, out, err = cli_run("db", "list", timeout=12)
            names = [ln.strip() for ln in (out or "").splitlines()
                     if ln.strip() and not ln.lower().startswith("database backups")]
            name = pick_list(disp, dev, "DB backups", names, empty=err or "No backups")
            if name and confirm(disp, dev, ["Restore database?", name]):
                show_message(disp, ["Restoring DB…", name])
                rc, out, err = cli_run("db", "restore", "--name", name, timeout=40)
                result_msg(disp, (err or out) if rc else "", "DB restored")
            return None
        if action == "reboot" and confirm(disp, dev, ["Reboot R4d-P4wn?"]):
            show_message(disp, ["Rebooting…"])
            cli_run("system", "reboot", timeout=8)
            return None
        if action == "shutdown" and confirm(disp, dev, ["Shutdown R4d-P4wn?"]):
            show_message(disp, ["Shutting down…"])
            cli_run("system", "shutdown", timeout=8)
            return None
        return None

    run_menu(disp, dev, menu, on_select=on_select, status_fn=status_fn)


def about_screen(disp: ST7789, dev: InputDevice):
    show_lines(disp, dev, "About", [
        "R4d-P4wn LCD GUI",
        "P4wnP1 A.L.O.A. by MaMe82",
        "modified by Son-of-David",
        "GNU GPL v3 or later",
        "",
        "USB WiFi Share VPN HID",
        "Kismet GPS Capture BT",
        "Templates LED System",
        "",
        "Web UI is still the full",
        "editor for scripts, VPN",
        "configs, and TriggerActions.",
        f"API {API_BASE}",
        f"CLI {CLI_BIN}",
    ])


def main_menu_screen(disp: ST7789, dev: InputDevice):
    menu = Menu("R4d-P4wn", [
        ("USB Settings", "usb"),
        ("WiFi", "wifi"),
        ("Share / VPN", "share"),
        ("HID Scripts", "hid"),
        ("Templates", "templates"),
        ("Capture", "capture"),
        ("Kismet", "kismet"),
        ("GPS", "gps"),
        ("Airgeddon", "airgeddon"),
        ("Bluetooth", "bluetooth"),
        ("Network", "network"),
        ("Trigger Actions", "trigger"),
        ("Event Log", "log"),
        ("Generic / System", "generic"),
        ("About / License", "about"),
    ])
    screens = {
        "usb": usb_screen,
        "wifi": wifi_screen,
        "share": share_screen,
        "hid": hid_screen,
        "templates": templates_screen,
        "capture": capture_screen,
        "kismet": kismet_screen,
        "gps": gps_screen,
        "airgeddon": airgeddon_screen,
        "bluetooth": bluetooth_screen,
        "network": network_screen,
        "trigger": trigger_screen,
        "log": log_screen,
        "generic": generic_screen,
        "about": about_screen,
    }

    def on_select(action: str) -> Optional[str]:
        fn = screens.get(action)
        if fn:
            fn(disp, dev)
        return None

    run_menu(disp, dev, menu, on_select=on_select,
             footer="ENTER=Status  F2=Select", timeout=0.6)


def main():
    dev = find_gpio_keys()
    disp = ST7789(**DISPLAY_CFG)
    in_menu = False
    try:
        draw_status_screen(disp, status_snapshot())
        last = time.time()
        while True:
            timeout = 2.0 if not in_menu else 0.4
            keys = wait_keys(dev, timeout)
            now = time.time()
            if not in_menu:
                if keys:
                    in_menu = True
                    main_menu_screen(disp, dev)
                    in_menu = False
                    draw_status_screen(disp, status_snapshot())
                    last = time.time()
                    continue
                if now - last >= 2.0:
                    draw_status_screen(disp, status_snapshot())
                    last = now
            else:
                continue
    except KeyboardInterrupt:
        pass
    finally:
        try:
            disp.close()
        except Exception:
            pass
        try:
            dev.close()
        except Exception:
            pass


if __name__ == "__main__":
    main()
