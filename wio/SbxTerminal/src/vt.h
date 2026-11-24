// A VT/xterm emulator sized for the Wio's screen: enough of xterm-256color for tmux, Claude Code,
// Codex and a login shell. Row 0 of each grid is the app's status bar; the terminal is below it.
#pragma once

#include <stddef.h>
#include <stdint.h>

// The grid's storage fits the smallest font; the current font sets how much of it is in use.
static const int MAX_COLS = 45;
static const int MAX_ROWS = 18;
extern int gridCols, gridRows, termRows;

enum : uint8_t { CELL_UNDERLINE = 1, CELL_WIDE_TAIL = 2, CELL_BOLD = 4 };

struct __attribute__((packed)) Cell {
  uint16_t ch;
  uint16_t fg;
  uint16_t bg;
  uint8_t flags;
};

struct Grid {
  Cell cells[MAX_ROWS][MAX_COLS];
  uint32_t dirty;  // one bit per row

  void fill(uint16_t fg, uint16_t bg);
  void put(int row, int col, uint16_t ch, uint16_t fg, uint16_t bg, uint8_t flags = 0);
};

constexpr uint16_t rgb565(uint8_t r, uint8_t g, uint8_t b) {
  return (uint16_t)((r & 0xF8) << 8 | (g & 0xFC) << 3 | b >> 3);
}

static const uint16_t VT_DEFAULT_FG = rgb565(0xF2, 0xF2, 0xF2);
static const uint16_t VT_DEFAULT_BG = rgb565(0x10, 0x10, 0x10);
static const uint16_t VT_CARET = rgb565(0xD7, 0x77, 0x57);  // Claude orange

// Columns a character takes in the terminal, as tmux counts them.
int charWidth(uint32_t cp);

class Vt {
 public:
  typedef void (*ReplyFn)(void *ctx, const uint8_t *data, size_t len);

  Vt(Grid *primary, Grid *alternate) : main_(primary), alt_(alternate) { reset(); }

  void setReply(ReplyFn fn, void *ctx) {
    reply_ = fn;
    replyCtx_ = ctx;
  }
  void reset();
  void feed(const uint8_t *data, size_t len);

  Grid *grid() const { return cur_; }
  int cursorRow() const { return row_; }
  int cursorCol() const { return col_ >= gridCols ? gridCols - 1 : col_; }
  bool cursorVisible() const { return cursorVisible_; }
  bool appCursor() const { return appCursor_; }
  bool mouseOn() const { return mouse_; }
  bool mouseSgr() const { return mouseSgr_; }

 private:
  enum State : uint8_t { GROUND, ESC, ESC_CHARSET, CSI, STRING, STRING_ESC };

  void byte(uint8_t b);
  void control(uint8_t b);
  void escape(uint8_t b);
  void csiDispatch(uint8_t final);
  void privateMode(bool on);
  void sgr();
  uint16_t extendedColor(int &i, bool &ok);
  void print(uint32_t cp);
  void lineFeed();
  void reverseIndex();
  void scrollUp(int top, int bottom, int n);
  void scrollDown(int top, int bottom, int n);
  void eraseCells(int row, int from, int to);
  void eraseLine(int mode);
  void eraseDisplay(int mode);
  void insertChars(int n);
  void deleteChars(int n);
  void moveTo(int row, int col);
  void switchScreen(bool alt, bool saveCursor);
  void saveCursor();
  void restoreCursor();
  void send(const char *text);
  int param(int i, int def) const;
  void dirtyRow(int row) { cur_->dirty |= 1UL << (row + 1); }
  Cell *line(int row) { return cur_->cells[row + 1]; }

  Grid *main_, *alt_, *cur_ = nullptr;
  ReplyFn reply_ = nullptr;
  void *replyCtx_ = nullptr;

  State state_ = GROUND;
  uint32_t utf8_ = 0;
  uint8_t utf8Left_ = 0;

  static const int MAX_PARAMS = 20;
  uint16_t params_[MAX_PARAMS];
  bool colon_[MAX_PARAMS];  // the parameter was joined to the one before it by ':'
  uint8_t paramCount_ = 0;
  bool paramStarted_ = false;
  char prefix_ = 0;        // '?', '>', '=' or '<'
  char intermediate_ = 0;  // '$', ' ' and so on
  char charsetSlot_ = 0;

  int row_ = 0, col_ = 0;
  bool wrapPending_ = false;
  int top_ = 0, bottom_ = termRows - 1;
  bool origin_ = false, autowrap_ = true, insert_ = false;
  bool cursorVisible_ = true, appCursor_ = false;
  bool mouse_ = false, mouseSgr_ = false;
  bool lineDrawing_[2] = {false, false};
  uint8_t charset_ = 0;
  uint32_t lastChar_ = ' ';
  uint32_t tabs_[2];

  int16_t fgIndex_ = -1;  // palette index, or -1 for the default or an RGB colour
  uint16_t fg_ = VT_DEFAULT_FG, bg_ = VT_DEFAULT_BG;
  bool bold_ = false, dim_ = false, underline_ = false, inverse_ = false, hidden_ = false;

  struct Saved {
    int row, col;
    int16_t fgIndex;
    uint16_t fg, bg;
    bool bold, dim, underline, inverse, hidden, origin;
  } saved_[2];
};
