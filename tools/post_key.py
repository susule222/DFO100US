# -*- coding: utf-8 -*-
"""Send one key to the game window via PostMessage (reaches the game even when
another window has the foreground).

Usage: python tools/post_key.py <key> [pid]
"""
import ctypes
import subprocess
import sys
import time
from ctypes import wintypes

user32 = ctypes.WinDLL('user32', use_last_error=True)
WM_KEYDOWN, WM_KEYUP = 0x100, 0x101
VK = {'ENTER': 0x0D, 'ESC': 0x1B, 'TAB': 0x09, 'SPACE': 0x20, 'M': 0x4D,
      'LEFT': 0x25, 'RIGHT': 0x27, 'UP': 0x26, 'DOWN': 0x28}


def main():
    key = sys.argv[1] if len(sys.argv) > 1 else 'ENTER'
    vk = VK.get(key.upper())
    if vk is None and len(key) == 1:
        vk = ord(key.upper())
    if vk is None:
        raise SystemExit('unknown key %s' % key)
    hwnd = user32.FindWindowW(None, 'Dungeon Fighter Online')
    if not hwnd:
        raise SystemExit('game window not found')
    user32.PostMessageW(hwnd, WM_KEYDOWN, vk, 0)
    time.sleep(0.08)
    user32.PostMessageW(hwnd, WM_KEYUP, vk, 0)
    print('posted key %s (vk %#x) to hwnd %d' % (key, vk, hwnd))


if __name__ == '__main__':
    main()
