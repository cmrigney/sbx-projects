#include "vt.h"

#include <stdio.h>
#include <string.h>

// The 16 ANSI colours of the iOS and web terminals.
static const uint32_t kAnsi[16] = {
    0x101E2B, 0xFF7B6E, 0x6FDC9A, 0xF5B94A, 0x7CC4FF, 0xD3A6FF, 0x85E8CD, 0xE4EFEC,
    0x5F7787, 0xFF9D93, 0x94E8B5, 0xFFD07A, 0xA5D6FF, 0xE2C2FF, 0xB0F2DF, 0xFFFFFF,
};

static uint16_t paletteColor(int i) {
  if (i < 16) {
    const uint32_t c = kAnsi[i];
    return rgb565(c >> 16, c >> 8, c);
  }
  if (i < 232) {
    static const uint8_t steps[6] = {0, 95, 135, 175, 215, 255};
    i -= 16;
    return rgb565(steps[i / 36], steps[(i / 6) % 6], steps[i % 6]);
  }
  const uint8_t v = 8 + (i - 232) * 10;
  return rgb565(v, v, v);
}

static uint16_t dimmed(uint16_t c) { return (c >> 1) & 0x7BEF; }

void Grid::fill(uint16_t fg, uint16_t bg) {
  for (int r = 0; r < GRID_ROWS; r++) {
    for (int c = 0; c < GRID_COLS; c++) cells[r][c] = Cell{' ', fg, bg, 0};
  }
  dirty = 0xFFFFFFFFUL >> (32 - GRID_ROWS);
}

void Grid::put(int row, int col, uint16_t ch, uint16_t fg, uint16_t bg, uint8_t flags) {
  if (row < 0 || row >= GRID_ROWS || col < 0 || col >= GRID_COLS) return;
  Cell &cell = cells[row][col];
  if (cell.ch == ch && cell.fg == fg && cell.bg == bg && cell.flags == flags) return;
  cell = Cell{ch, fg, bg, flags};
  dirty |= 1UL << row;
}

int charWidth(uint32_t cp) {
  if (cp < 0x300) return 1;
  if (cp <= 0x36F || (cp >= 0x200B && cp <= 0x200F) || (cp >= 0x2028 && cp <= 0x202E) ||
      (cp >= 0x2060 && cp <= 0x206F) || (cp >= 0xFE00 && cp <= 0xFE0F) || cp == 0xFEFF ||
      (cp >= 0x1AB0 && cp <= 0x1AFF) || (cp >= 0x20D0 && cp <= 0x20FF) ||
      (cp >= 0xE0100 && cp <= 0xE01EF)) {
    return 0;
  }
  if ((cp >= 0x1100 && cp <= 0x115F) || cp == 0x231A || cp == 0x231B || cp == 0x2329 ||
      cp == 0x232A || (cp >= 0x23E9 && cp <= 0x23EC) || cp == 0x23F0 || cp == 0x23F3 ||
      cp == 0x25FD || cp == 0x25FE || cp == 0x2614 || cp == 0x2615 ||
      (cp >= 0x2648 && cp <= 0x2653) || cp == 0x267F || cp == 0x2693 || cp == 0x26A1 ||
      cp == 0x26AA || cp == 0x26AB || cp == 0x26BD || cp == 0x26BE || cp == 0x26C4 ||
      cp == 0x26C5 || cp == 0x26CE || cp == 0x26D4 || cp == 0x26EA || cp == 0x26F2 ||
      cp == 0x26F3 || cp == 0x26F5 || cp == 0x26FA || cp == 0x26FD || cp == 0x2705 ||
      cp == 0x270A || cp == 0x270B || cp == 0x2728 || cp == 0x274C || cp == 0x274E ||
      (cp >= 0x2753 && cp <= 0x2755) || cp == 0x2757 || (cp >= 0x2795 && cp <= 0x2797) ||
      cp == 0x27B0 || cp == 0x27BF || cp == 0x2B1B || cp == 0x2B1C || cp == 0x2B50 ||
      cp == 0x2B55 || (cp >= 0x2E80 && cp <= 0x303E) || (cp >= 0x3041 && cp <= 0x33FF) ||
      (cp >= 0x3400 && cp <= 0x4DBF) || (cp >= 0x4E00 && cp <= 0x9FFF) ||
      (cp >= 0xA000 && cp <= 0xA4CF) || (cp >= 0xA960 && cp <= 0xA97F) ||
      (cp >= 0xAC00 && cp <= 0xD7A3) || (cp >= 0xF900 && cp <= 0xFAFF) ||
      (cp >= 0xFE30 && cp <= 0xFE6F) || (cp >= 0xFF00 && cp <= 0xFF60) ||
      (cp >= 0xFFE0 && cp <= 0xFFE6) || (cp >= 0x1F300 && cp <= 0x1F64F) ||
      (cp >= 0x1F680 && cp <= 0x1F6FF) || (cp >= 0x1F900 && cp <= 0x1F9FF) ||
      (cp >= 0x1FA70 && cp <= 0x1FAFF) || (cp >= 0x20000 && cp <= 0x3FFFD)) {
    return 2;
  }
  return 1;
}

void Vt::reset() {
  main_->fill(VT_DEFAULT_FG, VT_DEFAULT_BG);
  alt_->fill(VT_DEFAULT_FG, VT_DEFAULT_BG);
  cur_ = main_;
  state_ = GROUND;
  utf8Left_ = 0;
  row_ = col_ = 0;
  wrapPending_ = false;
  top_ = 0;
  bottom_ = TERM_ROWS - 1;
  origin_ = insert_ = appCursor_ = mouse_ = mouseSgr_ = false;
  autowrap_ = cursorVisible_ = true;
  lineDrawing_[0] = lineDrawing_[1] = false;
  charset_ = 0;
  lastChar_ = ' ';
  fgIndex_ = -1;
  fg_ = VT_DEFAULT_FG;
  bg_ = VT_DEFAULT_BG;
  bold_ = dim_ = underline_ = inverse_ = hidden_ = false;
  tabs_[0] = tabs_[1] = 0;
  for (int c = 8; c < GRID_COLS; c += 8) tabs_[c / 32] |= 1UL << (c % 32);
  saveCursor();
  saved_[1] = saved_[0];
}

void Vt::feed(const uint8_t *data, size_t len) {
  for (size_t i = 0; i < len; i++) byte(data[i]);
}

void Vt::send(const char *text) {
  if (reply_) reply_(replyCtx_, (const uint8_t *)text, strlen(text));
}

int Vt::param(int i, int def) const {
  if (i >= paramCount_ || params_[i] == 0) return def;
  return params_[i];
}

void Vt::byte(uint8_t b) {
  // Strings (OSC, DCS, APC, PM, SOS) are skipped whole, whatever they hold.
  if (state_ == STRING) {
    if (b == 0x07 || b == 0x9C) state_ = GROUND;
    else if (b == 0x1B) state_ = STRING_ESC;
    return;
  }
  if (state_ == STRING_ESC) {
    state_ = b == '\\' ? GROUND : STRING;
    return;
  }

  if (b < 0x20 || b == 0x7F) {
    if (b == 0x18 || b == 0x1A) {
      state_ = GROUND;
      utf8Left_ = 0;
    } else if (b == 0x1B) {
      state_ = ESC;
      utf8Left_ = 0;
    } else if (b != 0x7F) {
      control(b);
    }
    return;
  }

  switch (state_) {
    case GROUND:
      if (b < 0x80) {
        utf8Left_ = 0;
        uint32_t cp = b;
        if (lineDrawing_[charset_] && b >= 0x60 && b <= 0x7E) {
          static const uint16_t acs[31] = {
              0x25C6, 0x2592, 0x2409, 0x240C, 0x240D, 0x240A, 0x00B0, 0x00B1, 0x2424, 0x240B, 0x2518,
              0x2510, 0x250C, 0x2514, 0x253C, 0x23BA, 0x23BB, 0x2500, 0x23BC, 0x23BD, 0x251C, 0x2524,
              0x2534, 0x252C, 0x2502, 0x2264, 0x2265, 0x03C0, 0x2260, 0x00A3, 0x00B7};
          cp = acs[b - 0x60];
        }
        print(cp);
      } else if (b < 0xC0) {
        if (utf8Left_) {
          utf8_ = utf8_ << 6 | (b & 0x3F);
          if (--utf8Left_ == 0) print(utf8_);
        }
      } else if (b < 0xE0) {
        utf8_ = b & 0x1F;
        utf8Left_ = 1;
      } else if (b < 0xF0) {
        utf8_ = b & 0x0F;
        utf8Left_ = 2;
      } else if (b < 0xF8) {
        utf8_ = b & 0x07;
        utf8Left_ = 3;
      } else {
        utf8Left_ = 0;
      }
      break;
    case ESC:
      escape(b);
      break;
    case ESC_CHARSET:
      if (charsetSlot_ == '(' || charsetSlot_ == ')') lineDrawing_[charsetSlot_ == ')'] = b == '0';
      state_ = GROUND;
      break;
    case CSI:
      if (b >= '0' && b <= '9') {
        if (!paramStarted_) {
          if (paramCount_ == 0) {
            paramCount_ = 1;
            params_[0] = 0;
            colon_[0] = false;
          }
          paramStarted_ = true;
        }
        const uint32_t v = params_[paramCount_ - 1] * 10UL + (b - '0');
        params_[paramCount_ - 1] = v > 65535 ? 65535 : v;
      } else if (b == ';' || b == ':') {
        if (paramCount_ == 0) {
          paramCount_ = 1;
          params_[0] = 0;
          colon_[0] = false;
        }
        if (paramCount_ < MAX_PARAMS) {
          params_[paramCount_] = 0;
          colon_[paramCount_] = b == ':';
          paramCount_++;
        }
        paramStarted_ = true;
      } else if (b >= '<' && b <= '?') {
        prefix_ = b;
      } else if (b >= 0x20 && b <= 0x2F) {
        intermediate_ = b;
      } else if (b >= 0x40 && b <= 0x7E) {
        state_ = GROUND;
        csiDispatch(b);
      } else {
        state_ = GROUND;
      }
      break;
    default:
      state_ = GROUND;
      break;
  }
}

void Vt::control(uint8_t b) {
  switch (b) {
    case '\b':
      if (col_ >= GRID_COLS) col_ = GRID_COLS - 1;
      if (col_ > 0) col_--;
      wrapPending_ = false;
      break;
    case '\t': {
      int c = col_ >= GRID_COLS ? GRID_COLS - 1 : col_;
      for (c++; c < GRID_COLS - 1; c++) {
        if (tabs_[c / 32] & (1UL << (c % 32))) break;
      }
      col_ = c >= GRID_COLS ? GRID_COLS - 1 : c;
      wrapPending_ = false;
      break;
    }
    case '\n':
    case 0x0B:
    case 0x0C:
      lineFeed();
      break;
    case '\r':
      col_ = 0;
      wrapPending_ = false;
      break;
    case 0x0E:
      charset_ = 1;
      break;
    case 0x0F:
      charset_ = 0;
      break;
    default:
      break;
  }
}

void Vt::escape(uint8_t b) {
  state_ = GROUND;
  switch (b) {
    case '[':
      state_ = CSI;
      paramCount_ = 0;
      paramStarted_ = false;
      prefix_ = 0;
      intermediate_ = 0;
      break;
    case ']':
    case 'P':
    case '_':
    case '^':
    case 'X':
      state_ = STRING;
      break;
    case '(':
    case ')':
    case '*':
    case '+':
    case '#':
    case '%':
      charsetSlot_ = b;
      state_ = ESC_CHARSET;
      break;
    case '7':
      saveCursor();
      break;
    case '8':
      restoreCursor();
      break;
    case 'D':
      lineFeed();
      break;
    case 'E':
      col_ = 0;
      lineFeed();
      break;
    case 'H':
      if (col_ < GRID_COLS) tabs_[col_ / 32] |= 1UL << (col_ % 32);
      break;
    case 'M':
      reverseIndex();
      break;
    case 'c':
      reset();
      break;
    default:
      break;
  }
}

void Vt::moveTo(int row, int col) {
  const int minRow = origin_ ? top_ : 0;
  const int maxRow = origin_ ? bottom_ : TERM_ROWS - 1;
  row_ = row < minRow ? minRow : row > maxRow ? maxRow : row;
  col_ = col < 0 ? 0 : col >= GRID_COLS ? GRID_COLS - 1 : col;
  wrapPending_ = false;
}

void Vt::csiDispatch(uint8_t final) {
  if (intermediate_) return;  // cursor style, soft reset and the like change nothing here
  if (prefix_ == '?') {
    if (final == 'h') privateMode(true);
    else if (final == 'l') privateMode(false);
    else if (final == 'n' && param(0, 0) == 6) {
      char out[32];
      snprintf(out, sizeof(out), "\x1b[?%d;%dR", row_ + 1, cursorCol() + 1);
      send(out);
    }
    return;
  }
  if (prefix_ == '>') {
    if (final == 'c') send("\x1b[>0;95;0c");
    return;
  }
  if (prefix_) return;

  const int n = param(0, 1);
  const int col = col_ >= GRID_COLS ? GRID_COLS - 1 : col_;
  switch (final) {
    case 'A':
      row_ = row_ >= top_ ? (row_ - n < top_ ? top_ : row_ - n) : (row_ - n < 0 ? 0 : row_ - n);
      col_ = col;
      wrapPending_ = false;
      break;
    case 'B':
    case 'e': {
      const int limit = row_ <= bottom_ ? bottom_ : TERM_ROWS - 1;
      row_ = row_ + n > limit ? limit : row_ + n;
      col_ = col;
      wrapPending_ = false;
      break;
    }
    case 'C':
    case 'a':
      col_ = col + n >= GRID_COLS ? GRID_COLS - 1 : col + n;
      wrapPending_ = false;
      break;
    case 'D':
      col_ = col - n < 0 ? 0 : col - n;
      wrapPending_ = false;
      break;
    case 'E':
      moveTo(row_ + n, 0);
      break;
    case 'F':
      moveTo(row_ - n, 0);
      break;
    case 'G':
    case '`':
      col_ = n > GRID_COLS ? GRID_COLS - 1 : n - 1;
      wrapPending_ = false;
      break;
    case 'd':
      moveTo((origin_ ? top_ : 0) + n - 1, col);
      break;
    case 'H':
    case 'f':
      moveTo((origin_ ? top_ : 0) + param(0, 1) - 1, param(1, 1) - 1);
      break;
    case 'I':
      for (int i = 0; i < n; i++) control('\t');
      break;
    case 'J':
      eraseDisplay(param(0, 0));
      break;
    case 'K':
      eraseLine(param(0, 0));
      break;
    case 'L':
      if (row_ >= top_ && row_ <= bottom_) {
        scrollDown(row_, bottom_, n);
        col_ = 0;
        wrapPending_ = false;
      }
      break;
    case 'M':
      if (row_ >= top_ && row_ <= bottom_) {
        scrollUp(row_, bottom_, n);
        col_ = 0;
        wrapPending_ = false;
      }
      break;
    case 'P':
      deleteChars(n);
      break;
    case '@':
      insertChars(n);
      break;
    case 'X': {
      const int end = col + n > GRID_COLS ? GRID_COLS : col + n;
      eraseCells(row_, col, end);
      wrapPending_ = false;
      break;
    }
    case 'S':
      scrollUp(top_, bottom_, n);
      break;
    case 'T':
      scrollDown(top_, bottom_, n);
      break;
    case 'Z':
      for (int i = 0; i < n; i++) {
        int c = col_ >= GRID_COLS ? GRID_COLS - 1 : col_;
        for (c--; c > 0; c--) {
          if (tabs_[c / 32] & (1UL << (c % 32))) break;
        }
        col_ = c < 0 ? 0 : c;
      }
      wrapPending_ = false;
      break;
    case 'b':
      for (int i = 0; i < n && i < GRID_COLS * TERM_ROWS; i++) print(lastChar_);
      break;
    case 'c':
      if (param(0, 0) == 0) send("\x1b[?62;22c");
      break;
    case 'g':
      if (param(0, 0) == 3) tabs_[0] = tabs_[1] = 0;
      else if (param(0, 0) == 0) tabs_[col / 32] &= ~(1UL << (col % 32));
      break;
    case 'h':
      if (param(0, 0) == 4) insert_ = true;
      break;
    case 'l':
      if (param(0, 0) == 4) insert_ = false;
      break;
    case 'm':
      sgr();
      break;
    case 'n':
      if (param(0, 0) == 5) {
        send("\x1b[0n");
      } else if (param(0, 0) == 6) {
        char out[32];
        const int r = origin_ ? row_ - top_ : row_;
        snprintf(out, sizeof(out), "\x1b[%d;%dR", r + 1, cursorCol() + 1);
        send(out);
      }
      break;
    case 'r': {
      const int t = param(0, 1) - 1;
      const int b = (param(1, TERM_ROWS) > TERM_ROWS ? TERM_ROWS : param(1, TERM_ROWS)) - 1;
      if (t < b) {
        top_ = t;
        bottom_ = b;
        moveTo(origin_ ? top_ : 0, 0);
      }
      break;
    }
    case 's':
      saveCursor();
      break;
    case 'u':
      restoreCursor();
      break;
    default:
      break;
  }
}

void Vt::privateMode(bool on) {
  for (int i = 0; i < paramCount_; i++) {
    switch (params_[i]) {
      case 1:
        appCursor_ = on;
        break;
      case 6:
        origin_ = on;
        moveTo(on ? top_ : 0, 0);
        break;
      case 7:
        autowrap_ = on;
        if (!on) wrapPending_ = false;
        break;
      case 25:
        cursorVisible_ = on;
        break;
      case 47:
      case 1047:
        switchScreen(on, false);
        break;
      case 1048:
        if (on) saveCursor();
        else restoreCursor();
        break;
      case 1049:
        switchScreen(on, true);
        break;
      case 1000:
      case 1002:
      case 1003:
        mouse_ = on;
        break;
      case 1006:
        mouseSgr_ = on;
        break;
      default:
        break;
    }
  }
}

void Vt::switchScreen(bool alt, bool withCursor) {
  Grid *target = alt ? alt_ : main_;
  if (target == cur_) return;
  if (alt && withCursor) saveCursor();
  cur_ = target;
  cur_->dirty = 0xFFFFFFFFUL >> (32 - GRID_ROWS);
  if (alt) eraseDisplay(2);
  else if (withCursor) restoreCursor();
  wrapPending_ = false;
}

void Vt::saveCursor() {
  saved_[cur_ == alt_] = Saved{row_, col_, fgIndex_, fg_, bg_, bold_, dim_, underline_, inverse_, hidden_, origin_};
}

void Vt::restoreCursor() {
  const Saved &s = saved_[cur_ == alt_];
  row_ = s.row;
  col_ = s.col >= GRID_COLS ? GRID_COLS - 1 : s.col;
  fgIndex_ = s.fgIndex;
  fg_ = s.fg;
  bg_ = s.bg;
  bold_ = s.bold;
  dim_ = s.dim;
  underline_ = s.underline;
  inverse_ = s.inverse;
  hidden_ = s.hidden;
  origin_ = s.origin;
  wrapPending_ = false;
}

uint16_t Vt::extendedColor(int &i, bool &ok) {
  ok = false;
  // 38:2::r:g:b, 38:2:r:g:b and 38:5:n, with ':' joining them.
  if (i + 1 < paramCount_ && colon_[i + 1]) {
    int end = i + 1;
    while (end + 1 < paramCount_ && colon_[end + 1]) end++;
    const int count = end - i;
    const int kind = params_[i + 1];
    uint16_t out = 0;
    if (kind == 5 && count >= 2) {
      out = paletteColor(params_[i + 2] & 0xFF);
      ok = true;
    } else if (kind == 2 && count >= 4) {
      const int base = count >= 5 ? i + 3 : i + 2;
      out = rgb565(params_[base], params_[base + 1], params_[base + 2]);
      ok = true;
    }
    i = end;
    return out;
  }
  // 38;2;r;g;b and 38;5;n
  if (i + 2 < paramCount_ && params_[i + 1] == 5) {
    const uint16_t out = paletteColor(params_[i + 2] & 0xFF);
    i += 2;
    ok = true;
    return out;
  }
  if (i + 4 < paramCount_ && params_[i + 1] == 2) {
    const uint16_t out = rgb565(params_[i + 2], params_[i + 3], params_[i + 4]);
    i += 4;
    ok = true;
    return out;
  }
  i = paramCount_;
  return 0;
}

void Vt::sgr() {
  if (paramCount_ == 0) {
    params_[0] = 0;
    colon_[0] = false;
    paramCount_ = 1;
  }
  for (int i = 0; i < paramCount_; i++) {
    const int p = params_[i];
    switch (p) {
      case 0:
        fgIndex_ = -1;
        fg_ = VT_DEFAULT_FG;
        bg_ = VT_DEFAULT_BG;
        bold_ = dim_ = underline_ = inverse_ = hidden_ = false;
        break;
      case 1:
        bold_ = true;
        break;
      case 2:
        dim_ = true;
        break;
      case 4:
        underline_ = !(i + 1 < paramCount_ && colon_[i + 1] && params_[i + 1] == 0);
        while (i + 1 < paramCount_ && colon_[i + 1]) i++;
        break;
      case 7:
        inverse_ = true;
        break;
      case 8:
        hidden_ = true;
        break;
      case 21:
        underline_ = true;
        break;
      case 22:
        bold_ = dim_ = false;
        break;
      case 24:
        underline_ = false;
        break;
      case 27:
        inverse_ = false;
        break;
      case 28:
        hidden_ = false;
        break;
      case 38: {
        bool ok;
        const uint16_t c = extendedColor(i, ok);
        if (ok) {
          fg_ = c;
          fgIndex_ = -1;
        }
        break;
      }
      case 39:
        fg_ = VT_DEFAULT_FG;
        fgIndex_ = -1;
        break;
      case 48: {
        bool ok;
        const uint16_t c = extendedColor(i, ok);
        if (ok) bg_ = c;
        break;
      }
      case 49:
        bg_ = VT_DEFAULT_BG;
        break;
      case 58: {
        bool ok;
        extendedColor(i, ok);  // underline colour isn't drawn
        break;
      }
      default:
        if (p >= 30 && p <= 37) {
          fgIndex_ = p - 30;
          fg_ = paletteColor(fgIndex_);
        } else if (p >= 40 && p <= 47) {
          bg_ = paletteColor(p - 40);
        } else if (p >= 90 && p <= 97) {
          fgIndex_ = p - 90 + 8;
          fg_ = paletteColor(fgIndex_);
        } else if (p >= 100 && p <= 107) {
          bg_ = paletteColor(p - 100 + 8);
        }
        break;
    }
  }
}

void Vt::print(uint32_t cp) {
  const int width = charWidth(cp);
  if (width == 0) return;  // combining marks stay with the character before them
  lastChar_ = cp;

  if (wrapPending_ || (width == 2 && col_ == GRID_COLS - 1)) {
    if (autowrap_) {
      col_ = 0;
      wrapPending_ = false;
      lineFeed();
    } else if (width == 2) {
      return;
    }
  }
  if (col_ >= GRID_COLS) col_ = GRID_COLS - 1;
  if (insert_) insertChars(width);

  uint16_t fg = fg_;
  uint16_t bg = bg_;
  if (bold_ && fgIndex_ >= 0 && fgIndex_ < 8) fg = paletteColor(fgIndex_ + 8);
  if (dim_) fg = dimmed(fg);
  if (inverse_) {
    const uint16_t t = fg;
    fg = bg;
    bg = t;
  }
  if (hidden_) fg = bg;

  const uint16_t ch = cp > 0xFFFF ? 0xFFFD : (uint16_t)cp;
  const uint8_t flags = underline_ ? CELL_UNDERLINE : 0;
  Cell *l = line(row_);
  // Overwriting half of a wide character blanks its other half.
  if (l[col_].flags & CELL_WIDE_TAIL && col_ > 0) l[col_ - 1].ch = ' ';
  l[col_] = Cell{ch, fg, bg, flags};
  if (width == 2 && col_ + 1 < GRID_COLS) {
    l[col_ + 1] = Cell{' ', fg, bg, (uint8_t)(flags | CELL_WIDE_TAIL)};
  }
  const int next = col_ + width;
  if (next < GRID_COLS && l[next].flags & CELL_WIDE_TAIL) {
    l[next].ch = ' ';
    l[next].flags &= ~CELL_WIDE_TAIL;
  }
  dirtyRow(row_);

  if (next >= GRID_COLS) {
    col_ = GRID_COLS - 1;
    wrapPending_ = autowrap_;
  } else {
    col_ = next;
  }
}

void Vt::lineFeed() {
  if (col_ >= GRID_COLS) col_ = GRID_COLS - 1;
  wrapPending_ = false;
  if (row_ == bottom_) scrollUp(top_, bottom_, 1);
  else if (row_ < TERM_ROWS - 1) row_++;
}

void Vt::reverseIndex() {
  wrapPending_ = false;
  if (row_ == top_) scrollDown(top_, bottom_, 1);
  else if (row_ > 0) row_--;
}

void Vt::eraseCells(int row, int from, int to) {
  Cell *l = line(row);
  if (from > 0 && l[from].flags & CELL_WIDE_TAIL) l[from - 1].ch = ' ';
  if (to < GRID_COLS && l[to].flags & CELL_WIDE_TAIL) {
    l[to].ch = ' ';
    l[to].flags &= ~CELL_WIDE_TAIL;
  }
  for (int c = from; c < to; c++) l[c] = Cell{' ', VT_DEFAULT_FG, bg_, 0};
  dirtyRow(row);
}

void Vt::scrollUp(int top, int bottom, int n) {
  const int height = bottom - top + 1;
  if (n > height) n = height;
  if (n < height) {
    memmove(line(top), line(top + n), sizeof(Cell) * GRID_COLS * (height - n));
  }
  for (int r = bottom - n + 1; r <= bottom; r++) eraseCells(r, 0, GRID_COLS);
  for (int r = top; r <= bottom; r++) dirtyRow(r);
}

void Vt::scrollDown(int top, int bottom, int n) {
  const int height = bottom - top + 1;
  if (n > height) n = height;
  if (n < height) {
    memmove(line(top + n), line(top), sizeof(Cell) * GRID_COLS * (height - n));
  }
  for (int r = top; r < top + n; r++) eraseCells(r, 0, GRID_COLS);
  for (int r = top; r <= bottom; r++) dirtyRow(r);
}

void Vt::eraseLine(int mode) {
  const int col = col_ >= GRID_COLS ? GRID_COLS - 1 : col_;
  if (mode == 0) eraseCells(row_, col, GRID_COLS);
  else if (mode == 1) eraseCells(row_, 0, col + 1);
  else if (mode == 2) eraseCells(row_, 0, GRID_COLS);
  wrapPending_ = false;
}

void Vt::eraseDisplay(int mode) {
  if (mode == 0) {
    eraseLine(0);
    for (int r = row_ + 1; r < TERM_ROWS; r++) eraseCells(r, 0, GRID_COLS);
  } else if (mode == 1) {
    eraseLine(1);
    for (int r = 0; r < row_; r++) eraseCells(r, 0, GRID_COLS);
  } else if (mode == 2 || mode == 3) {
    for (int r = 0; r < TERM_ROWS; r++) eraseCells(r, 0, GRID_COLS);
  }
}

void Vt::insertChars(int n) {
  const int col = col_ >= GRID_COLS ? GRID_COLS - 1 : col_;
  if (n > GRID_COLS - col) n = GRID_COLS - col;
  Cell *l = line(row_);
  memmove(&l[col + n], &l[col], sizeof(Cell) * (GRID_COLS - col - n));
  eraseCells(row_, col, col + n);
  wrapPending_ = false;
}

void Vt::deleteChars(int n) {
  const int col = col_ >= GRID_COLS ? GRID_COLS - 1 : col_;
  if (n > GRID_COLS - col) n = GRID_COLS - col;
  Cell *l = line(row_);
  memmove(&l[col], &l[col + n], sizeof(Cell) * (GRID_COLS - col - n));
  eraseCells(row_, GRID_COLS - n, GRID_COLS);
  wrapPending_ = false;
}
