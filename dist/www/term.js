(function () {
  "use strict";

  var ANSI_FG = {
    30: "#000000", 31: "#cd0000", 32: "#00cd00", 33: "#cdcd00",
    34: "#5c5cff", 35: "#cd00cd", 36: "#00cdcd", 37: "#e5e5e5",
    90: "#7f7f7f", 91: "#ff0000", 92: "#00ff00", 93: "#ffff00",
    94: "#5c5cff", 95: "#ff00ff", 96: "#00ffff", 97: "#ffffff"
  };
  var ANSI_BG = {
    40: "#000000", 41: "#7f0000", 42: "#007f00", 43: "#7f007f",
    44: "#00007f", 45: "#7f007f", 46: "#007f7f", 47: "#c0c0c0",
    100: "#7f7f7f", 101: "#ff0000", 102: "#00ff00", 103: "#ffff00",
    104: "#0000ff", 105: "#ff00ff", 106: "#00ffff", 107: "#ffffff"
  };
  var MAX_SCROLL = 90;
  var FONT_PX = 13;
  var LINE_PX = 18;

  function escHtml(s) {
    return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  }

  function blankCell(attr) {
    return { ch: " ", fg: attr ? attr.fg : "", bg: attr ? attr.bg : "", bold: !!(attr && attr.bold) };
  }

  function cloneCell(c) {
    return { ch: c.ch, fg: c.fg, bg: c.bg, bold: c.bold };
  }

  function blankLine(cols, attr) {
    var a = new Array(cols);
    for (var i = 0; i < cols; i++) {
      a[i] = blankCell(attr);
    }
    return a;
  }

  function cloneLine(line) {
    var a = new Array(line.length);
    for (var i = 0; i < line.length; i++) {
      a[i] = cloneCell(line[i]);
    }
    return a;
  }

  function newScreen(cols, rows) {
    var s = new Array(rows);
    for (var r = 0; r < rows; r++) {
      s[r] = blankLine(cols);
    }
    return s;
  }

  function Term(cols, rows) {
    this.reset(cols, rows);
  }

  Term.prototype.resize = function (cols, rows) {
    cols = cols || this.cols;
    rows = rows || this.rows;
    if (cols === this.cols && rows === this.rows) {
      return;
    }
    function fit(src, c, r) {
      var dst = newScreen(c, r);
      var rr, cc;
      for (rr = 0; rr < r && src && rr < src.length; rr++) {
        for (cc = 0; cc < c && src[rr] && cc < src[rr].length; cc++) {
          dst[rr][cc] = cloneCell(src[rr][cc]);
        }
      }
      return dst;
    }
    this.screen = fit(this.screen, cols, rows);
    this.alt = fit(this.alt, cols, rows);
    this.cols = cols;
    this.rows = rows;
    if (this.top >= this.rows) { this.top = 0; }
    this.bottom = this.rows - 1;
    this.clampCursor();
  };

  Term.prototype.reset = function (cols, rows) {
    this.cols = cols || 80;
    this.rows = rows || 24;
    this.screen = newScreen(this.cols, this.rows);
    this.alt = newScreen(this.cols, this.rows);
    this.usingAlt = false;
    this.scrollback = [];
    this.cx = 0;
    this.cy = 0;
    this.saved = { cx: 0, cy: 0 };
    this.top = 0;
    this.bottom = this.rows - 1;
    this.fg = "";
    this.bg = "";
    this.bold = false;
    this.esc = 0;
    this.csi = "";
    this.osc = "";
    this.charsetSkip = 0;
  };

  Term.prototype.curScreen = function () {
    return this.usingAlt ? this.alt : this.screen;
  };

  Term.prototype.attr = function () {
    return { fg: this.fg, bg: this.bg, bold: this.bold };
  };

  Term.prototype.clampCursor = function () {
    if (this.cx < 0) { this.cx = 0; }
    if (this.cy < 0) { this.cy = 0; }
    if (this.cx >= this.cols) { this.cx = this.cols - 1; }
    if (this.cy >= this.rows) { this.cy = this.rows - 1; }
  };

  Term.prototype.scrollUp = function (n) {
    var scr = this.curScreen();
    var attr = this.attr();
    n = n || 1;
    while (n-- > 0) {
      var gone = scr[this.top];
      for (var r = this.top; r < this.bottom; r++) {
        scr[r] = scr[r + 1];
      }
      scr[this.bottom] = blankLine(this.cols, attr);
      if (!this.usingAlt && this.top === 0) {
        this.scrollback.push(cloneLine(gone));
        if (this.scrollback.length > MAX_SCROLL) {
          this.scrollback.splice(0, this.scrollback.length - MAX_SCROLL);
        }
      }
    }
  };

  Term.prototype.scrollDown = function (n) {
    var scr = this.curScreen();
    var attr = this.attr();
    n = n || 1;
    while (n-- > 0) {
      for (var r = this.bottom; r > this.top; r--) {
        scr[r] = scr[r - 1];
      }
      scr[this.top] = blankLine(this.cols, attr);
    }
  };

  Term.prototype.index = function () {
    if (this.cy === this.bottom) {
      this.scrollUp(1);
    } else if (this.cy < this.rows - 1) {
      this.cy++;
    }
  };

  Term.prototype.reverseIndex = function () {
    if (this.cy === this.top) {
      this.scrollDown(1);
    } else if (this.cy > 0) {
      this.cy--;
    }
  };

  Term.prototype.eraseInLine = function (mode) {
    var scr = this.curScreen();
    var line = scr[this.cy];
    var attr = this.attr();
    var x;
    if (!line) {
      return;
    }
    if (mode === 1) {
      for (x = 0; x <= this.cx && x < this.cols; x++) {
        line[x] = blankCell(attr);
      }
    } else if (mode === 2) {
      scr[this.cy] = blankLine(this.cols, attr);
    } else {
      for (x = this.cx; x < this.cols; x++) {
        line[x] = blankCell(attr);
      }
    }
  };

  Term.prototype.eraseInDisplay = function (mode) {
    var scr = this.curScreen();
    var attr = this.attr();
    var r;
    if (mode === 1) {
      this.eraseInLine(1);
      for (r = 0; r < this.cy; r++) {
        scr[r] = blankLine(this.cols, attr);
      }
    } else if (mode === 2 || mode === 3) {
      for (r = 0; r < this.rows; r++) {
        scr[r] = blankLine(this.cols, attr);
      }
      this.cx = 0;
      this.cy = 0;
      if (mode === 3) {
        this.scrollback = [];
      }
    } else {
      this.eraseInLine(0);
      for (r = this.cy + 1; r < this.rows; r++) {
        scr[r] = blankLine(this.cols, attr);
      }
    }
  };

  Term.prototype.switchAlt = function (on) {
    on = !!on;
    if (this.usingAlt === on) {
      return;
    }
    this.usingAlt = on;
    this.cx = 0;
    this.cy = 0;
    if (on) {
      this.alt = newScreen(this.cols, this.rows);
    }
  };

  Term.prototype.putChar = function (ch) {
    if (this.cx >= this.cols) {
      this.index();
      this.cx = 0;
    }
    var scr = this.curScreen();
    var line = scr[this.cy];
    if (!line) {
      return;
    }
    line[this.cx] = { ch: ch, fg: this.fg, bg: this.bg, bold: this.bold };
    this.cx++;
  };

  Term.prototype.tab = function () {
    this.cx = Math.min(this.cols - 1, (Math.floor(this.cx / 8) + 1) * 8);
  };

  Term.prototype.handleSgr = function (nums) {
    if (!nums.length) {
      nums = [0];
    }
    for (var i = 0; i < nums.length; i++) {
      var n = nums[i];
      if (n === 0 || isNaN(n)) {
        this.fg = "";
        this.bg = "";
        this.bold = false;
      } else if (n === 1) {
        this.bold = true;
      } else if (n === 22) {
        this.bold = false;
      } else if (n === 39) {
        this.fg = "";
      } else if (n === 49) {
        this.bg = "";
      } else if (ANSI_FG[n]) {
        this.fg = ANSI_FG[n];
      } else if (ANSI_BG[n]) {
        this.bg = ANSI_BG[n];
      } else if (n === 38 && nums[i + 1] === 5) {
        this.fg = "hsl(" + ((nums[i + 2] * 13) % 360) + ",70%,65%)";
        i += 2;
      } else if (n === 48 && nums[i + 1] === 5) {
        this.bg = "hsl(" + ((nums[i + 2] * 13) % 360) + ",50%,25%)";
        i += 2;
      } else if (n === 38 && nums[i + 1] === 2) {
        this.fg = "rgb(" + (nums[i + 2] || 0) + "," + (nums[i + 3] || 0) + "," + (nums[i + 4] || 0) + ")";
        i += 4;
      } else if (n === 48 && nums[i + 1] === 2) {
        this.bg = "rgb(" + (nums[i + 2] || 0) + "," + (nums[i + 3] || 0) + "," + (nums[i + 4] || 0) + ")";
        i += 4;
      }
    }
  };

  Term.prototype.parseNums = function (body) {
    if (body.charAt(0) === "?") {
      body = body.slice(1);
    }
    if (!body) {
      return [];
    }
    var parts = body.split(";");
    var out = [];
    for (var i = 0; i < parts.length; i++) {
      out.push(parts[i] === "" ? 0 : parseInt(parts[i], 10));
    }
    return out;
  };

  Term.prototype.handleCsi = function (body, final) {
    var priv = body.charAt(0) === "?";
    var p = this.parseNums(body);
    var n = p[0] || 0;
    var m = p[1] || 0;
    var scr, r, x, line, attr;
    if (priv && (final === "h" || final === "l")) {
      var enable = final === "h";
      if (n === 1049 || n === 1047 || n === 47) {
        this.switchAlt(enable);
      }
      return;
    }
    switch (final) {
      case "A":
        this.cy = Math.max(0, this.cy - (n || 1));
        break;
      case "B":
        this.cy = Math.min(this.rows - 1, this.cy + (n || 1));
        break;
      case "C":
        this.cx = Math.min(this.cols - 1, this.cx + (n || 1));
        break;
      case "D":
        this.cx = Math.max(0, this.cx - (n || 1));
        break;
      case "G":
        this.cx = Math.max(0, Math.min(this.cols - 1, (n || 1) - 1));
        break;
      case "d":
        this.cy = Math.max(0, Math.min(this.rows - 1, (n || 1) - 1));
        break;
      case "H":
      case "f":
        this.cy = Math.max(0, Math.min(this.rows - 1, (n || 1) - 1));
        this.cx = Math.max(0, Math.min(this.cols - 1, (m || 1) - 1));
        break;
      case "J":
        this.eraseInDisplay(n);
        break;
      case "K":
        this.eraseInLine(n);
        break;
      case "m":
        this.handleSgr(p.length ? p : [0]);
        break;
      case "r":
        this.top = Math.max(0, (n || 1) - 1);
        this.bottom = Math.min(this.rows - 1, (m || this.rows) - 1);
        if (this.top >= this.bottom) {
          this.top = 0;
          this.bottom = this.rows - 1;
        }
        break;
      case "S":
        this.scrollUp(n || 1);
        break;
      case "T":
        this.scrollDown(n || 1);
        break;
      case "L":
        scr = this.curScreen();
        attr = this.attr();
        n = n || 1;
        for (r = this.bottom; r >= this.cy + n; r--) {
          scr[r] = scr[r - n];
        }
        for (r = this.cy; r < this.cy + n && r <= this.bottom; r++) {
          scr[r] = blankLine(this.cols, attr);
        }
        break;
      case "M":
        scr = this.curScreen();
        attr = this.attr();
        n = n || 1;
        for (r = this.cy; r <= this.bottom - n; r++) {
          scr[r] = scr[r + n];
        }
        for (r = Math.max(this.cy, this.bottom - n + 1); r <= this.bottom; r++) {
          scr[r] = blankLine(this.cols, attr);
        }
        break;
      case "@":
        line = this.curScreen()[this.cy];
        attr = this.attr();
        n = n || 1;
        if (line) {
          for (x = this.cols - 1; x >= this.cx + n; x--) {
            line[x] = line[x - n];
          }
          for (x = this.cx; x < this.cx + n && x < this.cols; x++) {
            line[x] = blankCell(attr);
          }
        }
        break;
      case "P":
        line = this.curScreen()[this.cy];
        attr = this.attr();
        n = n || 1;
        if (line) {
          for (x = this.cx; x < this.cols - n; x++) {
            line[x] = line[x + n];
          }
          for (x = Math.max(this.cx, this.cols - n); x < this.cols; x++) {
            line[x] = blankCell(attr);
          }
        }
        break;
      case "X":
        line = this.curScreen()[this.cy];
        attr = this.attr();
        n = n || 1;
        if (line) {
          for (x = this.cx; x < this.cx + n && x < this.cols; x++) {
            line[x] = blankCell(attr);
          }
        }
        break;
      default:
        break;
    }
  };

  Term.prototype.handleEsc = function (ch) {
    if (ch === "7") {
      this.saved = { cx: this.cx, cy: this.cy };
    } else if (ch === "8") {
      this.cx = this.saved.cx;
      this.cy = this.saved.cy;
      this.clampCursor();
    } else if (ch === "M") {
      this.reverseIndex();
    } else if (ch === "D") {
      this.index();
    } else if (ch === "E") {
      this.cx = 0;
      this.index();
    } else if (ch === "c") {
      var cols = this.cols;
      var rows = this.rows;
      this.reset(cols, rows);
    }
  };

  Term.prototype.write = function (s) {
    if (!s) {
      return;
    }
    var i = 0;
    while (i < s.length) {
      if (this.charsetSkip) {
        this.charsetSkip--;
        i++;
        continue;
      }
      if (this.esc === 3) {
        var oc = s.charAt(i);
        i++;
        if (oc === "\x07" || (this.osc.slice(-1) === "\x1b" && oc === "\\")) {
          this.esc = 0;
          this.osc = "";
        } else {
          this.osc += oc;
          if (this.osc.length > 512) {
            this.esc = 0;
            this.osc = "";
          }
        }
        continue;
      }
      if (this.esc === 2) {
        var cc = s.charAt(i);
        i++;
        if ((cc >= "0" && cc <= "9") || cc === ";" || cc === "?" || cc === ":" || cc === ">") {
          this.csi += cc;
          if (this.csi.length > 64) {
            this.esc = 0;
            this.csi = "";
          }
        } else {
          this.handleCsi(this.csi, cc);
          this.esc = 0;
          this.csi = "";
        }
        continue;
      }
      if (this.esc === 1) {
        var e = s.charAt(i);
        i++;
        if (e === "[") {
          this.esc = 2;
          this.csi = "";
        } else if (e === "]") {
          this.esc = 3;
          this.osc = "";
        } else if (e === "(" || e === ")" || e === "*" || e === "+") {
          this.esc = 0;
          this.charsetSkip = 1;
        } else {
          this.handleEsc(e);
          this.esc = 0;
        }
        continue;
      }
      var code = s.charCodeAt(i);
      if (code === 0x1b) {
        this.esc = 1;
        i++;
        continue;
      }
      if (code === 0 || code === 7) {
        i++;
        continue;
      }
      if (code === 8) {
        if (this.cx > 0) {
          this.cx--;
        }
        i++;
        continue;
      }
      if (code === 9) {
        this.tab();
        i++;
        continue;
      }
      if (code === 10 || code === 11 || code === 12) {
        this.index();
        i++;
        continue;
      }
      if (code === 13) {
        this.cx = 0;
        i++;
        continue;
      }
      if (code === 127) {
        if (this.cx > 0) {
          this.cx--;
          this.curScreen()[this.cy][this.cx] = blankCell(this.attr());
        }
        i++;
        continue;
      }
      var ch = s.charAt(i);
      if (code >= 0xd800 && code <= 0xdbff && i + 1 < s.length) {
        ch += s.charAt(i + 1);
        i += 2;
      } else {
        i++;
      }
      this.putChar(ch);
    }
  };

  Term.prototype.lineHtml = function (line, cursorAt) {
    if (!line) {
      return "";
    }
    var html = "";
    var i = 0;
    while (i < line.length) {
      var cell = line[i];
      var j = i + 1;
      if (i !== cursorAt) {
        while (j < line.length && j !== cursorAt && line[j].fg === cell.fg && line[j].bg === cell.bg && line[j].bold === cell.bold) {
          j++;
        }
      }
      var text = "";
      for (var k = i; k < j; k++) {
        text += line[k].ch || " ";
      }
      var st = "";
      if (i === cursorAt) {
        st += "background:#b6f2b6;color:#111;";
      } else {
        if (cell.fg) { st += "color:" + cell.fg + ";"; }
        if (cell.bg) { st += "background:" + cell.bg + ";"; }
        if (cell.bold) { st += "font-weight:bold;"; }
      }
      html += st ? '<span style="' + st + '">' + escHtml(text) + "</span>" : escHtml(text);
      i = j;
    }
    return html;
  };

  Term.prototype.toHtml = function () {
    var lines = [];
    var r;
    if (!this.usingAlt) {
      for (r = 0; r < this.scrollback.length; r++) {
        lines.push(this.lineHtml(this.scrollback[r], -1));
      }
    }
    var scr = this.curScreen();
    for (r = 0; r < this.rows; r++) {
      lines.push(this.lineHtml(scr[r], r === this.cy ? this.cx : -1));
    }
    return lines.join("\n");
  };

  window.R4dTerm = Term;

  var NAMED_KEYS = {
    Enter: "\r",
    Backspace: "\x7f",
    Tab: "\t",
    Escape: "\x1b",
    ArrowUp: "\x1b[A",
    ArrowDown: "\x1b[B",
    ArrowLeft: "\x1b[D",
    ArrowRight: "\x1b[C",
    Delete: "\x1b[3~",
    Home: "\x1b[H",
    End: "\x1b[F",
    PageUp: "\x1b[5~",
    PageDown: "\x1b[6~"
  };

  var PRESS_KEYS = {
    "ctrl-c": "\x03",
    "up": "\x1b[A",
    "down": "\x1b[B",
    "left": "\x1b[D",
    "right": "\x1b[C"
  };

  window.r4dTermKeyBytes = function (e) {
    if (!e || e.isComposing || e.keyCode === 229) {
      return "";
    }
    if (e.altKey || e.metaKey) {
      return "";
    }
    if (e.ctrlKey) {
      var ck = e.key || "";
      if (ck.length === 1) {
        var c = ck.toLowerCase().charCodeAt(0);
        if (c >= 97 && c <= 122) {
          return String.fromCharCode(c - 96);
        }
      }
      if (e.keyCode >= 65 && e.keyCode <= 90) {
        return String.fromCharCode(e.keyCode - 64);
      }
      return "";
    }
    if (NAMED_KEYS[e.key]) {
      return NAMED_KEYS[e.key];
    }
    if (e.keyCode === 8) {
      return "\x7f";
    }
    if (e.keyCode === 13) {
      return "\r";
    }
    if (e.keyCode === 9) {
      return "\t";
    }
    if (e.keyCode === 27) {
      return "\x1b";
    }
    if (e.keyCode === 37) { return "\x1b[D"; }
    if (e.keyCode === 38) { return "\x1b[A"; }
    if (e.keyCode === 39) { return "\x1b[C"; }
    if (e.keyCode === 40) { return "\x1b[B"; }
    return "";
  };

  window.r4dOpenTermWS = function (path, handlers) {
    handlers = handlers || {};
    var proto = location.protocol === "https:" ? "wss:" : "ws:";
    var ws = new WebSocket(proto + "//" + location.host + path);
    ws.binaryType = "arraybuffer";
    var dec = new TextDecoder("utf-8");
    ws.onmessage = function (ev) {
      if (typeof ev.data === "string") {
        var msg = null;
        try { msg = JSON.parse(ev.data); } catch (err) { return; }
        if (msg && msg.t === "reset") {
          if (handlers.onReset) { handlers.onReset(); }
          return;
        }
        if (handlers.onHello) { handlers.onHello(msg || {}); }
        return;
      }
      var text = dec.decode(ev.data, { stream: true });
      if (text && handlers.onData) {
        handlers.onData(text);
      }
    };
    ws.onopen = function () { if (handlers.onOpen) { handlers.onOpen(); } };
    ws.onclose = function () { if (handlers.onClose) { handlers.onClose(); } };
    ws.onerror = function () { if (handlers.onError) { handlers.onError(); } };
    return {
      sendRaw: function (s) {
        if (ws.readyState === 1 && s) { ws.send(s); }
      },
      resize: function (cols, rows) {
        if (ws.readyState === 1) {
          ws.send(JSON.stringify({ t: "resize", cols: cols, rows: rows }));
        }
      },
      close: function () {
        try { ws.close(); } catch (err) {}
      },
      ready: function () { return ws.readyState === 1; }
    };
  };

  if (!document.getElementById("r4d-term-css")) {
    var st = document.createElement("style");
    st.id = "r4d-term-css";
    st.textContent =
      "html,body{touch-action:manipulation;}" +
      ".r4d-term-kb{display:block;width:100%;margin-top:8px;padding:12px;font-size:16px;line-height:1.35;border:1px solid #555;background:#1a1a1a;color:#b6f2b6;border-radius:4px;outline:none;box-sizing:border-box;-webkit-appearance:none;font-family:monospace;touch-action:manipulation;}" +
      ".r4d-term-kb:focus{border-color:#4fc3f7;}" +
      ".r4d-term-keys{display:grid;grid-template-columns:1fr 1fr 1fr;grid-template-areas:'ctrl ctrl ctrl' '. up .' 'left down right';gap:8px;margin-top:10px;touch-action:manipulation;user-select:none;-webkit-user-select:none;}" +
      ".r4d-term-keys button{min-height:58px;padding:8px;background:#333;color:#eee;border:0;border-radius:8px;font-size:28px;line-height:1;touch-action:manipulation;-webkit-tap-highlight-color:transparent;}" +
      ".r4d-term-keys button.accent{grid-area:ctrl;background:#f2a93b;color:#111;font-weight:700;font-size:18px;}" +
      ".r4d-term-keys .k-up{grid-area:up;}" +
      ".r4d-term-keys .k-down{grid-area:down;}" +
      ".r4d-term-keys .k-left{grid-area:left;}" +
      ".r4d-term-keys .k-right{grid-area:right;}";
    document.head.appendChild(st);
  }

  window.r4dTermInputHtml =
    '<textarea ref="termInput" class="r4d-term-kb" rows="1"' +
    ' autocomplete="off" autocorrect="off" autocapitalize="none" spellcheck="false"' +
    ' inputmode="text" enterkeyhint="enter"' +
    ' placeholder="Tap here to type with a phone or computer keyboard"' +
    ' @keydown="onTermKey" @input="onTermInput" @beforeinput="onTermBeforeInput"' +
    ' @compositionstart="onComposeStart" @compositionend="onComposeEnd"' +
    ' @focus="onTermFocus" @blur="onTermBlur"></textarea>' +
    '<div class="r4d-term-keys">' +
    '<button type="button" class="accent" @touchstart.prevent="pressNamed(\'ctrl-c\')" @mousedown.prevent="pressNamed(\'ctrl-c\')">Ctrl+C</button>' +
    '<button type="button" class="k-up" @touchstart.prevent="pressNamed(\'up\')" @mousedown.prevent="pressNamed(\'up\')">↑</button>' +
    '<button type="button" class="k-left" @touchstart.prevent="pressNamed(\'left\')" @mousedown.prevent="pressNamed(\'left\')">←</button>' +
    '<button type="button" class="k-down" @touchstart.prevent="pressNamed(\'down\')" @mousedown.prevent="pressNamed(\'down\')">↓</button>' +
    '<button type="button" class="k-right" @touchstart.prevent="pressNamed(\'right\')" @mousedown.prevent="pressNamed(\'right\')">→</button>' +
    '</div>';

  window.r4dTermMixin = {
    data: function () {
      return {
        termFocused: false,
        follow: true,
        emu: new Term(80, 24),
        cols: 80,
        rows: 24,
        sock: null,
        resizeTimer: null,
        reconnectTimer: null,
        _raf: 0,
        _alive: true,
        _composing: false,
        _pressLock: false
      };
    },
    computed: {
      termStyle: function () {
        var border = this.termFocused ? "#4fc3f7" : "#333";
        return "margin-top:12px;height:58vh;max-height:520px;overflow-y:scroll;overflow-x:auto;-webkit-overflow-scrolling:touch;overscroll-behavior:contain;background:#111;border:2px solid " + border + ";outline:none;cursor:text";
      }
    },
    methods: {
      termPreEl: function () {
        return this.$refs.termPre || (this.$refs.termBox && this.$refs.termBox.querySelector("pre"));
      },
      paintTerm: function () {
        if (this._pendingOut) {
          this.emu.write(this._pendingOut);
          this._pendingOut = "";
        }
        var pre = this.termPreEl();
        if (pre) {
          pre.innerHTML = this.emu.toHtml() || " ";
        }
        if (this.follow) {
          this.scrollBottom();
        }
      },
      schedulePaint: function () {
        if (this._raf) { return; }
        var self = this;
        this._raf = requestAnimationFrame(function () {
          self._raf = 0;
          self.paintTerm();
        });
      },
      scrollBottom: function () {
        var el = this.$refs.termBox;
        if (el) { el.scrollTop = el.scrollHeight; }
      },
      toggleFollow: function () {
        this.follow = !this.follow;
        if (this.follow) { this.scrollBottom(); }
      },
      onTermScroll: function (e) {
        var el = e.currentTarget;
        this.follow = (el.scrollHeight - el.scrollTop - el.clientHeight) < 32;
      },
      onTermWheel: function (e) {
        e.stopPropagation();
      },
      clearTermInput: function () {
        var ta = this.$refs.termInput;
        if (ta && ta.value) {
          ta.value = "";
        }
      },
      focusTermInput: function () {
        var ta = this.$refs.termInput;
        this.termFocused = true;
        if (!ta) { return; }
        if (document.activeElement === ta) { return; }
        ta.focus();
      },
      focusTerm: function () {
        this.focusTermInput();
      },
      onTermFocus: function () {
        this.termFocused = true;
      },
      onTermBlur: function () {
        var self = this;
        setTimeout(function () {
          var ae = document.activeElement;
          if (ae !== self.$refs.termInput) {
            self.termFocused = false;
          }
        }, 80);
      },
      sendTyped: function (s) {
        if (!s) { return; }
        var now = Date.now();
        if (this._lastTyped === s && now - (this._lastTypedAt || 0) < 40) {
          this.clearTermInput();
          return;
        }
        this._lastTyped = s;
        this._lastTypedAt = now;
        this.sendRaw(s);
        this.clearTermInput();
      },
      onComposeStart: function () {
        this._composing = true;
      },
      onComposeEnd: function () {
        this._composing = false;
        var ta = this.$refs.termInput;
        var v = ta ? ta.value : "";
        if (v) { this.sendTyped(v); }
        else { this.clearTermInput(); }
      },
      onTermBeforeInput: function (e) {
        var t = e.inputType || "";
        if (t === "insertText" || t === "insertCompositionText" || t === "insertReplacementText") {
          if (e.data) {
            e.preventDefault();
            this._composing = false;
            this.sendTyped(e.data);
          }
          return;
        }
        if (t === "deleteContentBackward" || t === "deleteContentForward") {
          e.preventDefault();
          this.sendTyped(t === "deleteContentForward" ? "\x1b[3~" : "\x7f");
        } else if (t === "insertLineBreak" || t === "insertParagraph") {
          e.preventDefault();
          this.sendTyped("\r");
        }
      },
      onTermInput: function (e) {
        var t = (e && e.inputType) || "";
        if (t === "deleteContentBackward") {
          this.sendTyped("\x7f");
          return;
        }
        if (t === "insertLineBreak" || t === "insertParagraph") {
          this.sendTyped("\r");
          return;
        }
        var ta = this.$refs.termInput;
        var v = ta ? ta.value : "";
        if (v) { this.sendTyped(v); }
      },
      measureSize: function () {
        var el = this.$refs.termBox;
        if (!el) {
          return { cols: this.cols, rows: this.rows };
        }
        var pad = 24;
        var w = Math.max(40, el.clientWidth - pad);
        var h = Math.max(40, el.clientHeight - pad);
        var probe = el._probe;
        if (!probe) {
          probe = document.createElement("span");
          probe.style.cssText = "position:absolute;left:-9999px;top:0;font-family:monospace;font-size:" + FONT_PX + "px;line-height:" + LINE_PX + "px;white-space:pre;";
          probe.textContent = "0123456789";
          el.appendChild(probe);
          el._probe = probe;
        }
        var cw = probe.offsetWidth / 10;
        if (!cw || cw < 4) { cw = 8; }
        return {
          cols: Math.max(20, Math.floor(w / cw)),
          rows: Math.max(8, Math.floor(h / LINE_PX))
        };
      },
      scheduleResize: function () {
        var self = this;
        if (this.resizeTimer) { clearTimeout(this.resizeTimer); }
        this.resizeTimer = setTimeout(function () { self.measureAndResize(false); }, 150);
      },
      measureAndResize: function (force) {
        var sz = this.measureSize();
        if (!force && sz.cols === this.cols && sz.rows === this.rows) {
          return;
        }
        if (!force && sz.cols === this.cols && Math.abs(sz.rows - this.rows) <= 8) {
          return;
        }
        this.cols = sz.cols;
        this.rows = sz.rows;
        this.emu.resize(this.cols, this.rows);
        this.schedulePaint();
        if (this.sock && this.sock.ready()) {
          this.sock.resize(this.cols, this.rows);
        }
      },
      sendRaw: function (s) {
        if (this.sock && this.sock.ready()) {
          this.sock.sendRaw(s);
          return true;
        }
        return false;
      },
      sendCtrlC: function () {
        this.sendRaw("\x03");
        this.focusTermInput();
      },
      pressNamed: function (name) {
        if (this._pressLock) { return; }
        this._pressLock = true;
        var self = this;
        setTimeout(function () { self._pressLock = false; }, 80);
        var s = PRESS_KEYS[name];
        if (s) { this.sendRaw(s); }
      },
      onTermKey: function (e) {
        var s = window.r4dTermKeyBytes(e);
        if (!s) { return; }
        e.preventDefault();
        e.stopPropagation();
        this.sendRaw(s);
        this.clearTermInput();
      },
      connectTerm: function () {
        var self = this;
        if (!this._alive || !this.termWsPath) { return; }
        if (this.sock) {
          this.sock.close();
          this.sock = null;
        }
        this.sock = window.r4dOpenTermWS(this.termWsPath, {
          onHello: function (msg) {
            if (self.status) {
              self.status = Object.assign({}, self.status, { running: !!msg.running });
            }
            if (self.sock && self.sock.ready()) {
              self.sock.resize(self.cols, self.rows);
            }
            self.err = "";
          },
          onReset: function () {
            self.emu.reset(self.cols, self.rows);
            self.schedulePaint();
          },
          onData: function (text) {
            self._pendingOut = (self._pendingOut || "") + text;
            self.schedulePaint();
          },
          onOpen: function () {
            self.err = "";
          },
          onClose: function () {
            if (!self._alive) { return; }
            self.reconnectTimer = setTimeout(function () { self.connectTerm(); }, 400);
          },
          onError: function () {}
        });
      },
      bootTerm: function () {
        var self = this;
        this._alive = true;
        this.$nextTick(function () {
          self.measureAndResize(true);
          self.focusTermInput();
          self.connectTerm();
        });
        this._onWinResize = function () { self.scheduleResize(); };
        window.addEventListener("resize", this._onWinResize);
      },
      teardownTerm: function () {
        this._alive = false;
        if (this.sock) { this.sock.close(); this.sock = null; }
        if (this.resizeTimer) { clearTimeout(this.resizeTimer); }
        if (this.reconnectTimer) { clearTimeout(this.reconnectTimer); }
        if (this._raf) { cancelAnimationFrame(this._raf); this._raf = 0; }
        if (this._onWinResize) {
          window.removeEventListener("resize", this._onWinResize);
        }
      }
    }
  };

})();
