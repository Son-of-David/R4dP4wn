#!/usr/bin/env python3
#Simple ST7789 Games GUI
#- Shows a System Status screen on startup
#- On any button press, shows a Games menu (Snake, Tetris, Paddle)
#- Controls (menu): UP/DOWN to move, F2 to select, ENTER to return to Status
#- Controls (games): See footer on each game screen

import sys
import time
import os
import shutil
import subprocess
import json
import random
from datetime import datetime
from typing import Optional, Tuple, List, Dict

from evdev import InputDevice, list_devices, ecodes
from PIL import Image, ImageDraw, ImageFont
from st7789_userland import ST7789

#-Optional: allow passing an explicit input device path
DEVICE_PATH = sys.argv[1] if len(sys.argv) > 1 else None

WIDTH, HEIGHT = 240, 240
DISPLAY_CFG = dict(spi_speed=12_000_000, rotation=90, bgr=False, x_start=0, y_start=0)

LINE_HEIGHT = 22

#-Fonts
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

ImageFont = ImageFont  # type hint workaround
FONT_SMALL = load_font(16)
FONT_MED   = load_font(20)

#-Input device discovery
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

#-System status helpers
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
            return f"{days}d {hours:02d}:{minutes:02d}:{seconds:02d}"
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
        total_gib = total / (1024**3)
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

def draw_status_screen(disp: ST7789):
    img = Image.new("RGB", (WIDTH, HEIGHT), "BLACK")
    d = ImageDraw.Draw(img)
    y = 8
    d.text((WIDTH//2, y), "System Status", font=FONT_MED, fill=(0, 220, 255), anchor="mm"); y += 26
    now = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    d.text((8, y), f"Time: {now}"[:28], font=FONT_SMALL, fill=(200,255,255)); y += LINE_HEIGHT
    d.text((8, y), f"Load: {read_loadavg_percent()}"[:28], font=FONT_SMALL, fill=(255,255,255)); y += LINE_HEIGHT
    mem_pct, mem_total = read_mem_usage()
    d.text((8, y), f"Memory: {mem_pct}% of {mem_total}"[:28], font=FONT_SMALL, fill=(255,255,255)); y += LINE_HEIGHT
    d.text((8, y), f"CPU Temp: {read_cpu_temp()}"[:28], font=FONT_SMALL, fill=(255,255,255)); y += LINE_HEIGHT
    d.text((8, y), f"Uptime: {read_uptime()}"[:28], font=FONT_SMALL, fill=(255,255,255)); y += LINE_HEIGHT
    d.text((WIDTH//2, HEIGHT-18), "Press any key for Games", font=FONT_SMALL, fill=(180,180,180), anchor="mm")
    disp.display(img)

#-Menu
class Menu:
    def __init__(self, title: str, items: List[Tuple[str, str]]):
        self.title = title
        self.items = items
        self.index = 0
    def move(self, delta: int):
        self.index = (self.index + delta) % len(self.items)
    def current(self) -> Tuple[str, str]:
        return self.items[self.index]

def draw_menu(disp: ST7789, menu: Menu):
    img = Image.new("RGB", (WIDTH, HEIGHT), "BLACK")
    d = ImageDraw.Draw(img)
    d.text((WIDTH//2, 20), menu.title, font=FONT_MED, fill=(255,255,0), anchor="mm")
    start_y = 60
    box_w = WIDTH - 30
    box_h = 30
    gap = 8
    for i, (label, _) in enumerate(menu.items):
        y = start_y + i * (box_h + gap)
        x = (WIDTH - box_w) // 2
        if i == menu.index:
            d.rectangle((x, y, x + box_w, y + box_h), outline=(0, 255, 255), width=2)
            fill = (255, 255, 255)
        else:
            d.rectangle((x, y, x + box_w, y + box_h), outline=(120, 120, 120), width=1)
            fill = (200, 200, 200)
        d.text((WIDTH//2, y + box_h//2), label[:26], font=FONT_SMALL, fill=fill, anchor="mm")
    d.text((WIDTH//2, HEIGHT-16), "Press=Status Key2=Enter", font=FONT_SMALL, fill=(180,180,180), anchor="mm")
    disp.display(img)

def show_message(disp: ST7789, lines: List[str], hold_sec: float = 1.0):
    img = Image.new("RGB", (WIDTH, HEIGHT), "BLACK")
    d = ImageDraw.Draw(img)
    y = 20
    for ln in lines:
        d.text((WIDTH//2, y), ln[:26], font=FONT_SMALL, fill=(255,255,255), anchor="mm")
        y += 24
    disp.display(img)
    if hold_sec > 0:
        time.sleep(hold_sec)

############################
#-Snake
############################
CELL_SNAKE = 12
GRID_W = WIDTH // CELL_SNAKE
GRID_H = HEIGHT // CELL_SNAKE

def _snake_draw_cell(draw: ImageDraw.ImageDraw, gx: int, gy: int, color: Tuple[int,int,int]):
    x0 = gx * CELL_SNAKE
    y0 = gy * CELL_SNAKE
    x1 = x0 + CELL_SNAKE - 1
    y1 = y0 + CELL_SNAKE - 1
    draw.rectangle((x0, y0, x1, y1), fill=color)

def _snake_new_food(snake: List[Tuple[int,int]]) -> Tuple[int,int]:
    free = [(x,y) for x in range(GRID_W) for y in range(GRID_H) if (x,y) not in snake]
    return random.choice(free) if free else (0,0)

def snake_run(disp: ST7789, dev: InputDevice):
    def init_state():
        start_x = GRID_W // 2
        start_y = GRID_H // 2
        snake = [(start_x, start_y), (start_x-1, start_y), (start_x-2, start_y)]
        direction = (1, 0)
        food = _snake_new_food(snake)
        paused = False
        score = 0
        move_interval = 0.18
        last_move = time.time()
        return snake, direction, food, paused, score, move_interval, last_move

    def render(snake, food, paused, score):
        img = Image.new("RGB", (WIDTH, HEIGHT), "BLACK")
        d = ImageDraw.Draw(img)
        _snake_draw_cell(d, food[0], food[1], (255, 60, 60))
        for i, (sx, sy) in enumerate(snake):
            color = (0, 220, 50) if i > 0 else (0, 255, 120)
            _snake_draw_cell(d, sx, sy, color)
        d.text((4, 2), f"Score:{score}", fill=(255,255,255))
        if paused:
            d.text((WIDTH//2, HEIGHT//2), "PAUSED", fill=(255,255,255), anchor="mm")
        d.text((WIDTH//2, HEIGHT-16), "F2 Pause  ENTER Exit", fill=(180,180,180), anchor="mm")
        disp.display(img)

    def wait_game_over(score):
        img = Image.new("RGB", (WIDTH, HEIGHT), "BLACK")
        d = ImageDraw.Draw(img)
        d.text((WIDTH//2, HEIGHT//2 - 10), "GAME OVER", fill=(255,255,255), anchor="mm")
        d.text((WIDTH//2, HEIGHT//2 + 14), f"Score {score}", fill=(255,255,255), anchor="mm")
        d.text((WIDTH//2, HEIGHT//2 + 36), "F2 Restart  ENTER Exit", fill=(255,255,255), anchor="mm")
        disp.display(img)
        for e in dev.read_loop():
            if e.type != ecodes.EV_KEY or e.value != 1:
                continue
            if e.code == ecodes.KEY_F2:
                return "restart"
            if e.code == ecodes.KEY_ENTER:
                return "exit"

    while True:
        snake, direction, food, paused, score, move_interval, last_move = init_state()
        render(snake, food, paused, score)
        for event in dev.read_loop():
            now = time.time()
            # Movement tick (single grid step per tick)
            if not paused and (now - last_move) >= move_interval:
                last_move = now
                nx = (snake[0][0] + direction[0]) % GRID_W
                ny = (snake[0][1] + direction[1]) % GRID_H
                new_head = (nx, ny)
                if new_head in snake:
                    result = wait_game_over(score)
                    if result == "exit":
                        return
                    else:
                        break  # restart
                snake.insert(0, new_head)
                if new_head == food:
                    score += 1
                    food = _snake_new_food(snake)
                else:
                    snake.pop()
                render(snake, food, paused, score)

            if event.type != ecodes.EV_KEY:
                continue
            if event.code == ecodes.KEY_ENTER and event.value == 1:
                return
            if event.code == ecodes.KEY_F2 and event.value == 1:
                paused = not paused
                render(snake, food, paused, score)
                continue
            if event.value != 1 or paused:
                continue
            # no 180-degree reversal
            if event.code == ecodes.KEY_UP and direction != (0, 1):
                direction = (0, -1)
            elif event.code == ecodes.KEY_DOWN and direction != (0, -1):
                direction = (0, 1)
            elif event.code == ecodes.KEY_LEFT and direction != (1, 0):
                direction = (-1, 0)
            elif event.code == ecodes.KEY_RIGHT and direction != (-1, 0):
                direction = (1, 0)
        else:
            return  # device ended unexpectedly -> exit loop and game

############################
#-Tetris
############################
COLS = 10
ROWS = 20
CELL_TET = 12
BOARD_W = COLS * CELL_TET
BOARD_H = ROWS * CELL_TET
OFFSET_X = (WIDTH - BOARD_W) // 2
OFFSET_Y = (HEIGHT - BOARD_H) // 2
COLORS_TET = [(0,240,240),(0,0,240),(240,160,0),(240,240,0),(0,240,0),(240,0,240),(240,0,0)]
PIECES = {
    'I': [[(0,1),(1,1),(2,1),(3,1)],[(2,0),(2,1),(2,2),(2,3)],[(0,2),(1,2),(2,2),(3,2)],[(1,0),(1,1),(1,2),(1,3)]],
    'J': [[(0,0),(0,1),(1,1),(2,1)],[(1,0),(2,0),(1,1),(1,2)],[(0,1),(1,1),(2,1),(2,2)],[(1,0),(1,1),(0,2),(1,2)]],
    'L': [[(2,0),(0,1),(1,1),(2,1)],[(1,0),(1,1),(1,2),(2,2)],[(0,1),(1,1),(2,1),(0,2)],[(0,0),(1,0),(1,1),(1,2)]],
    'O': [[(1,0),(2,0),(1,1),(2,1)]]*4,
    'S': [[(1,0),(2,0),(0,1),(1,1)],[(1,0),(1,1),(2,1),(2,2)],[(1,1),(2,1),(0,2),(1,2)],[(0,0),(0,1),(1,1),(1,2)]],
    'T': [[(1,0),(0,1),(1,1),(2,1)],[(1,0),(1,1),(2,1),(1,2)],[(0,1),(1,1),(2,1),(1,2)],[(1,0),(0,1),(1,1),(1,2)]],
    'Z': [[(0,0),(1,0),(1,1),(2,1)],[(2,0),(1,1),(2,1),(1,2)],[(0,1),(1,1),(1,2),(2,2)],[(1,0),(0,1),(1,1),(0,2)]],
}
ORDER = ['I','J','L','O','S','T','Z']

def _t_draw_block(d: ImageDraw.ImageDraw, gx: int, gy: int, color: Tuple[int,int,int]):
    x0 = OFFSET_X + gx * CELL_TET
    y0 = OFFSET_Y + gy * CELL_TET
    x1 = x0 + CELL_TET - 1
    y1 = y0 + CELL_TET - 1
    d.rectangle((x0, y0, x1, y1), fill=color)
    d.rectangle((x0, y0, x1, y1), outline=(30,30,30))

def _t_can_place(board: List[List[int]], shape: List[Tuple[int,int]], ox: int, oy: int) -> bool:
    for (sx, sy) in shape:
        x = ox + sx
        y = oy + sy
        if x < 0 or x >= COLS or y < 0 or y >= ROWS:
            return False
        if board[y][x] != -1:
            return False
    return True

def _t_lock_piece(board: List[List[int]], shape: List[Tuple[int,int]], ox: int, oy: int, color_idx: int):
    for (sx, sy) in shape:
        x = ox + sx
        y = oy + sy
        if 0 <= x < COLS and 0 <= y < ROWS:
            board[y][x] = color_idx

def _t_clear_lines(board: List[List[int]]) -> int:
    new_rows = [row for row in board if any(cell == -1 for cell in row)]
    cleared = ROWS - len(new_rows)
    while len(new_rows) < ROWS:
        new_rows.insert(0, [-1]*COLS)
    board[:] = new_rows
    return cleared

def _t_new_piece() -> Tuple[str, int, List[Tuple[int,int]]]:
    p = random.choice(ORDER)
    rot = random.randint(0, 3)
    shape = PIECES[p][rot]
    return p, rot, shape

def _t_rotate_piece(piece: str, rot: int, dir: int) -> Tuple[int, List[Tuple[int,int]]]:
    rot = (rot + dir) % 4
    return rot, PIECES[piece][rot]

def tetris_run(disp: ST7789, dev: InputDevice):
    board = [[-1 for _ in range(COLS)] for _ in range(ROWS)]
    score = 0
    paused = False
    piece, rot, shape = _t_new_piece()
    ox = COLS // 2 - 2
    oy = 0
    color_idx = ORDER.index(piece)
    fall_interval = 0.5
    fast_interval = 0.08
    last_fall = time.time()
    soft_drop = False

    def render():
        img = Image.new("RGB", (WIDTH, HEIGHT), "BLACK")
        d = ImageDraw.Draw(img)
        d.rectangle((OFFSET_X-1, OFFSET_Y-1, OFFSET_X+BOARD_W, OFFSET_Y+BOARD_H), outline=(30,30,30))
        for y in range(ROWS):
            for x in range(COLS):
                cell = board[y][x]
                if cell != -1:
                    _t_draw_block(d, x, y, COLORS_TET[cell])
        if not paused:
            for (sx, sy) in shape:
                _t_draw_block(d, ox+sx, oy+sy, COLORS_TET[color_idx])
        d.text((4, 4), f"Score:{score}", fill=(255,255,255))
        if paused:
            d.text((WIDTH//2, HEIGHT//2), "PAUSED", fill=(255,255,255), anchor="mm")
        d.text((WIDTH//2, HEIGHT-16), "F2 Pause  ENTER Exit", fill=(180,180,180), anchor="mm")
        disp.display(img)

    render()
    for event in dev.read_loop():
        now = time.time()
        interval = fast_interval if soft_drop else fall_interval
        if not paused and (now - last_fall) >= interval:
            last_fall = now
            if _t_can_place(board, shape, ox, oy+1):
                oy += 1
            else:
                _t_lock_piece(board, shape, ox, oy, color_idx)
                cleared = _t_clear_lines(board)
                if cleared > 0:
                    score += cleared * 100
                piece, rot, shape = _t_new_piece()
                ox = COLS // 2 - 2
                oy = 0
                color_idx = ORDER.index(piece)
                if not _t_can_place(board, shape, ox, oy):
                    # Game over screen
                    img = Image.new("RGB", (WIDTH, HEIGHT), "BLACK")
                    d = ImageDraw.Draw(img)
                    d.text((WIDTH//2, HEIGHT//2 - 10), "GAME OVER", fill=(255,255,255), anchor="mm")
                    d.text((WIDTH//2, HEIGHT//2 + 14), f"Score {score}", fill=(255,255,255), anchor="mm")
                    d.text((WIDTH//2, HEIGHT//2 + 36), "ENTER Exit", fill=(255,255,255), anchor="mm")
                    disp.display(img)
                    # Wait exit
                    for e in dev.read_loop():
                        if e.type == ecodes.EV_KEY and e.value == 1 and e.code == ecodes.KEY_ENTER:
                            return
            render()

        if event.type != ecodes.EV_KEY:
            continue
        if event.code == ecodes.KEY_ENTER and event.value == 1:
            return
        if event.code == ecodes.KEY_F2 and event.value == 1:
            paused = not paused
            render()
            continue
        if event.value != 1 or paused:
            if event.code == ecodes.KEY_DOWN and event.value == 0:
                soft_drop = False
            continue
        if event.code == ecodes.KEY_LEFT:
            if _t_can_place(board, shape, ox-1, oy):
                ox -= 1
                render()
        elif event.code == ecodes.KEY_RIGHT:
            if _t_can_place(board, shape, ox+1, oy):
                ox += 1
                render()
        elif event.code == ecodes.KEY_UP:
            new_rot = (rot + 1) % 4
            new_shape = PIECES[ORDER[color_idx]][new_rot] if ORDER[color_idx] == ORDER[color_idx] else PIECES[ORDER[color_idx]][new_rot]
            # simple rotate with basic wall-kicks
            tried = False
            for dx in (0, -1, +1, -2, +2):
                if _t_can_place(board, new_shape, ox+dx, oy):
                    rot = new_rot
                    shape = new_shape
                    ox += dx
                    tried = True
                    break
            if not tried:
                pass
            render()
        elif event.code == ecodes.KEY_DOWN:
            soft_drop = True

############################
#-Paddle (single-player)
############################
def paddle_run(disp: ST7789, dev: InputDevice):
    # Pixel-based simple paddle game
    paddle_w = 50
    paddle_h = 6
    paddle_y = HEIGHT - 20
    paddle_x = (WIDTH - paddle_w) // 2
    paddle_speed = 8

    ball_r = 4
    ball_x = WIDTH // 2
    ball_y = HEIGHT // 2
    vel_x = 3
    vel_y = 3
    paused = False
    score = 0
    last_update = time.time()
    frame_interval = 0.015  # ~66 FPS if events arrive frequently

    def render():
        img = Image.new("RGB", (WIDTH, HEIGHT), "BLACK")
        d = ImageDraw.Draw(img)
        # paddle
        d.rectangle((paddle_x, paddle_y, paddle_x + paddle_w, paddle_y + paddle_h), fill=(200,200,200))
        # ball
        d.ellipse((ball_x - ball_r, ball_y - ball_r, ball_x + ball_r, ball_y + ball_r), fill=(255,180,0))
        # hud
        d.text((4, 4), f"Score:{score}", fill=(255,255,255))
        if paused:
            d.text((WIDTH//2, HEIGHT//2), "PAUSED", fill=(255,255,255), anchor="mm")
        d.text((WIDTH//2, HEIGHT-16), "LEFT/RIGHT move  F2 Pause  ENTER Exit", font=FONT_SMALL, fill=(180,180,180), anchor="mm")
        disp.display(img)

    def game_over():
        img = Image.new("RGB", (WIDTH, HEIGHT), "BLACK")
        d = ImageDraw.Draw(img)
        d.text((WIDTH//2, HEIGHT//2 - 10), "GAME OVER", fill=(255,255,255), anchor="mm")
        d.text((WIDTH//2, HEIGHT//2 + 14), f"Score {score}", fill=(255,255,255), anchor="mm")
        d.text((WIDTH//2, HEIGHT//2 + 36), "ENTER Exit", fill=(255,255,255), anchor="mm")
        disp.display(img)
        for e in dev.read_loop():
            if e.type == ecodes.EV_KEY and e.value == 1 and e.code == ecodes.KEY_ENTER:
                return

    render()
    for event in dev.read_loop():
        now = time.time()
        if not paused and (now - last_update) >= frame_interval:
            last_update = now
            # move ball
            ball_x += vel_x
            ball_y += vel_y
            # wall bounce
            if ball_x - ball_r <= 0:
                ball_x = ball_r
                vel_x = abs(vel_x)
            if ball_x + ball_r >= WIDTH:
                ball_x = WIDTH - ball_r
                vel_x = -abs(vel_x)
            if ball_y - ball_r <= 0:
                ball_y = ball_r
                vel_y = abs(vel_y)
            # paddle collision
            if paddle_y - ball_r <= ball_y <= paddle_y + paddle_h + ball_r:
                if paddle_x - ball_r <= ball_x <= paddle_x + paddle_w + ball_r and vel_y > 0:
                    ball_y = paddle_y - ball_r
                    vel_y = -abs(vel_y)
                    # tweak angle based on hit position
                    hit_pos = (ball_x - paddle_x) / max(1, paddle_w)
                    vel_x = int((hit_pos - 0.5) * 10)
                    if vel_x == 0:
                        vel_x = random.choice([-2, 2])
                    score += 1
            # miss -> game over
            if ball_y - ball_r > HEIGHT:
                game_over()
                return
            render()

        if event.type != ecodes.EV_KEY:
            continue
        if event.code == ecodes.KEY_ENTER and event.value == 1:
            return
        if event.code == ecodes.KEY_F2 and event.value == 1:
            paused = not paused
            render()
            continue
        if paused:
            continue
        if event.value == 1:
            if event.code == ecodes.KEY_LEFT:
                paddle_x = max(0, paddle_x - paddle_speed)
                render()
            elif event.code == ecodes.KEY_RIGHT:
                paddle_x = min(WIDTH - paddle_w, paddle_x + paddle_speed)
                render()

############################
#-Main
############################
GAMES_MENU = Menu("Games", [
    ("Snake", "snake"),
    ("Tetris", "tetris"),
    ("Paddle", "paddle"),
])

def main():
    dev = find_gpio_keys()
    disp = ST7789(**DISPLAY_CFG)

    in_menu = False
    try:
        draw_status_screen(disp)
        for event in dev.read_loop():
            if event.type != ecodes.EV_KEY:
                continue
            if not in_menu:
                # Any keydown shows the Games menu
                if event.value == 1:
                    in_menu = True
                    GAMES_MENU.index = 0
                    draw_menu(disp, GAMES_MENU)
                continue

            # In menu
            if event.value != 1:
                continue
            if event.code == ecodes.KEY_UP:
                GAMES_MENU.move(-1)
                draw_menu(disp, GAMES_MENU)
            elif event.code == ecodes.KEY_DOWN:
                GAMES_MENU.move(1)
                draw_menu(disp, GAMES_MENU)
            elif event.code == ecodes.KEY_F2:
                label, sel = GAMES_MENU.current()
                if sel == "snake":
                    snake_run(disp, dev)
                elif sel == "tetris":
                    tetris_run(disp, dev)
                elif sel == "paddle":
                    paddle_run(disp, dev)
                draw_menu(disp, GAMES_MENU)  # return to menu after game finishes
            elif event.code == ecodes.KEY_ENTER:
                # Return to status; next key press brings menu again
                in_menu = False
                draw_status_screen(disp)
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
