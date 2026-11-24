// Turns HID keyboard usages into keys, and keys into the bytes a terminal sends.
#pragma once

#include <stdio.h>
#include <string.h>

#include "platform.h"

static const uint8_t HID_USAGE_CAPS_LOCK = 0x39;
static const uint8_t HID_USAGE_ROLLOVER = 0x01;

// US layout. Index = usage - 0x1E, covering 0x1E (1) .. 0x38 (/).
static const char kHidUnshifted[] = "1234567890\n\x1B\b\t -=[]\\#;'`,./";
static const char kHidShifted[] = "!@#$%^&*()\n\x1B\b\t _+{}|~:\"~<>?";
// Keypad 0x54 (/) .. 0x63 (.), ignoring Num Lock.
static const char kHidKeypad[] = "/*-+\n1234567890.";

inline bool hidToInput(uint8_t usage, uint8_t mods, bool caps, Input &out) {
  out.mods = mods;
  out.ch = 0;
  const bool shift = mods & MOD_SHIFT;
  char c = 0;
  if (usage >= 0x04 && usage <= 0x1D) {
    c = 'a' + (usage - 0x04);
    if (shift != caps) c -= 'a' - 'A';
  } else if (usage >= 0x1E && usage <= 0x38) {
    c = (shift ? kHidShifted : kHidUnshifted)[usage - 0x1E];
  } else if (usage >= 0x54 && usage <= 0x63) {
    c = kHidKeypad[usage - 0x54];
  } else if (usage >= 0x3A && usage <= 0x45) {
    out.key = (Key)(KEY_F1 + (usage - 0x3A));
    return true;
  } else {
    switch (usage) {
      case 0x49: out.key = KEY_INSERT; return true;
      case 0x4A: out.key = KEY_HOME; return true;
      case 0x4B: out.key = KEY_PGUP; return true;
      case 0x4C: out.key = KEY_DELETE; return true;
      case 0x4D: out.key = KEY_END; return true;
      case 0x4E: out.key = KEY_PGDN; return true;
      case 0x4F: out.key = KEY_RIGHT; return true;
      case 0x50: out.key = KEY_LEFT; return true;
      case 0x51: out.key = KEY_DOWN; return true;
      case 0x52: out.key = KEY_UP; return true;
      default: return false;
    }
  }
  switch (c) {
    case '\n': out.key = KEY_ENTER; break;
    case 0x1B: out.key = KEY_ESC; break;
    case '\b': out.key = KEY_BACKSPACE; break;
    case '\t': out.key = KEY_TAB; break;
    default:
      out.key = KEY_TEXT;
      out.ch = c;
      break;
  }
  return true;
}

// xterm's modifier parameter: 1 + shift + 2*alt + 4*ctrl.
inline int xtermModifier(uint8_t mods) {
  return 1 + ((mods & MOD_SHIFT) ? 1 : 0) + ((mods & MOD_ALT) ? 2 : 0) + ((mods & MOD_CTRL) ? 4 : 0);
}

// Writes what the key sends to the terminal into out (at least 16 bytes) and returns the length.
inline size_t keyToBytes(const Input &in, bool appCursor, char *out) {
  const int mod = xtermModifier(in.mods);
  const bool alt = in.mods & MOD_ALT;
  size_t n = 0;

  const auto simple = [&](char c) {
    if (alt) out[n++] = 0x1B;
    out[n++] = c;
    return n;
  };
  const auto arrow = [&](char final) {
    if (mod > 1) return (size_t)snprintf(out, 16, "\x1b[1;%d%c", mod, final);
    return (size_t)snprintf(out, 16, appCursor ? "\x1bO%c" : "\x1b[%c", final);
  };
  const auto tilde = [&](int code) {
    if (mod > 1) return (size_t)snprintf(out, 16, "\x1b[%d;%d~", code, mod);
    return (size_t)snprintf(out, 16, "\x1b[%d~", code);
  };

  switch (in.key) {
    case KEY_TEXT: {
      char c = in.ch;
      if (in.mods & MOD_CTRL) {
        if (c >= 'a' && c <= 'z') c = c - 'a' + 1;
        else if (c >= 'A' && c <= 'Z') c = c - 'A' + 1;
        else if (c == ' ' || c == '2' || c == '@') c = 0;
        else if (c == '[' || c == '3') c = 0x1B;
        else if (c == '\\' || c == '4') c = 0x1C;
        else if (c == ']' || c == '5') c = 0x1D;
        else if (c == '^' || c == '6') c = 0x1E;
        else if (c == '_' || c == '-' || c == '7') c = 0x1F;
        else if (c == '/') c = 0x1F;
        else if (c == '8' || c == '?') c = 0x7F;
      }
      return simple(c);
    }
    case KEY_ENTER: return simple('\r');
    case KEY_ESC: return simple(0x1B);
    case KEY_BACKSPACE: return simple((in.mods & MOD_CTRL) ? 0x08 : 0x7F);
    case KEY_TAB:
      if (in.mods & MOD_SHIFT) {
        memcpy(out, "\x1b[Z", 3);
        return 3;
      }
      return simple('\t');
    case KEY_UP: return arrow('A');
    case KEY_DOWN: return arrow('B');
    case KEY_RIGHT: return arrow('C');
    case KEY_LEFT: return arrow('D');
    case KEY_HOME: return arrow('H');
    case KEY_END: return arrow('F');
    case KEY_INSERT: return tilde(2);
    case KEY_DELETE: return tilde(3);
    case KEY_PGUP: return tilde(5);
    case KEY_PGDN: return tilde(6);
    default: break;
  }
  if (in.key >= KEY_F1 && in.key <= KEY_F12) {
    const int f = in.key - KEY_F1;
    if (f < 4) {
      if (mod > 1) return (size_t)snprintf(out, 16, "\x1b[1;%d%c", mod, 'P' + f);
      return (size_t)snprintf(out, 16, "\x1bO%c", 'P' + f);
    }
    static const uint8_t codes[8] = {15, 17, 18, 19, 20, 21, 23, 24};
    return tilde(codes[f - 4]);
  }
  return 0;
}
