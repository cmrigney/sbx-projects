#include "render.h"

#include "font5x7.h"

static const int CELL_W = 6;
static const int MARGIN_X = 1;

enum : uint8_t { SEG_L = 1, SEG_R = 2, SEG_U = 4, SEG_D = 8 };

// Which arms a box-drawing character has. Heavy and double lines are drawn as single ones.
static uint8_t boxSegments(uint16_t c) {
  struct Range {
    uint16_t first, last;
    uint8_t segs;
  };
  static const Range ranges[] = {
      {0x2500, 0x2501, 3},  {0x2502, 0x2503, 12}, {0x2504, 0x2505, 3},  {0x2506, 0x2507, 12},
      {0x2508, 0x2509, 3},  {0x250A, 0x250B, 12}, {0x250C, 0x250F, 10}, {0x2510, 0x2513, 9},
      {0x2514, 0x2517, 6},  {0x2518, 0x251B, 5},  {0x251C, 0x2523, 14}, {0x2524, 0x252B, 13},
      {0x252C, 0x2533, 11}, {0x2534, 0x253B, 7},  {0x253C, 0x254B, 15}, {0x254C, 0x254D, 3},
      {0x254E, 0x254F, 12}, {0x2550, 0x2550, 3},  {0x2551, 0x2551, 12}, {0x2552, 0x2554, 10},
      {0x2555, 0x2557, 9},  {0x2558, 0x255A, 6},  {0x255B, 0x255D, 5},  {0x255E, 0x2560, 14},
      {0x2561, 0x2563, 13}, {0x2564, 0x2566, 11}, {0x2567, 0x2569, 7},  {0x256A, 0x256C, 15},
      {0x256D, 0x256D, 10}, {0x256E, 0x256E, 9},  {0x256F, 0x256F, 5},  {0x2570, 0x2570, 6},
      {0x2574, 0x2574, 1},  {0x2575, 0x2575, 4},  {0x2576, 0x2576, 2},  {0x2577, 0x2577, 8},
      {0x2578, 0x2578, 1},  {0x2579, 0x2579, 4},  {0x257A, 0x257A, 2},  {0x257B, 0x257B, 8},
      {0x257C, 0x257C, 3},  {0x257D, 0x257D, 12}, {0x257E, 0x257E, 3},  {0x257F, 0x257F, 12},
      {0x23BF, 0x23BF, 6},  // the corner Claude Code puts under a tool call
  };
  for (const Range &r : ranges) {
    if (c >= r.first && c <= r.last) return r.segs;
  }
  return 0;
}

struct Symbol {
  uint16_t cp;
  uint8_t columns[5];
};

// Symbols agents' interfaces use, in the font's format: five columns, low bit on top.
static const Symbol kSymbols[] = {
    {0x00B7, {0x00, 0x00, 0x08, 0x00, 0x00}}, {0x2022, {0x00, 0x1C, 0x1C, 0x1C, 0x00}},
    {0x2026, {0x40, 0x00, 0x40, 0x00, 0x40}}, {0x2190, {0x08, 0x1C, 0x2A, 0x08, 0x08}},
    {0x2191, {0x04, 0x02, 0x7F, 0x02, 0x04}}, {0x2192, {0x08, 0x08, 0x2A, 0x1C, 0x08}},
    {0x2193, {0x10, 0x20, 0x7F, 0x20, 0x10}}, {0x21B5, {0x10, 0x38, 0x54, 0x10, 0x1F}},
    {0x23F5, {0x00, 0x7F, 0x3E, 0x1C, 0x08}}, {0x23FA, {0x1C, 0x3E, 0x3E, 0x3E, 0x1C}},
    {0x25A0, {0x3E, 0x3E, 0x3E, 0x3E, 0x3E}}, {0x25A1, {0x3E, 0x22, 0x22, 0x22, 0x3E}},
    {0x25B2, {0x30, 0x3C, 0x3F, 0x3C, 0x30}}, {0x25B6, {0x00, 0x7F, 0x3E, 0x1C, 0x08}},
    {0x25BC, {0x03, 0x0F, 0x3F, 0x0F, 0x03}}, {0x25C0, {0x08, 0x1C, 0x3E, 0x7F, 0x00}},
    {0x25C6, {0x08, 0x1C, 0x3E, 0x1C, 0x08}}, {0x25CB, {0x1C, 0x22, 0x22, 0x22, 0x1C}},
    {0x25CF, {0x1C, 0x3E, 0x3E, 0x3E, 0x1C}}, {0x25EF, {0x1C, 0x22, 0x22, 0x22, 0x1C}},
    {0x2713, {0x10, 0x20, 0x10, 0x0C, 0x03}}, {0x2714, {0x10, 0x30, 0x18, 0x0C, 0x03}},
    {0x2717, {0x22, 0x14, 0x08, 0x14, 0x22}}, {0x2718, {0x22, 0x14, 0x08, 0x14, 0x22}},
    {0x276F, {0x00, 0x22, 0x14, 0x08, 0x00}}, {0x2022, {0x00, 0x1C, 0x1C, 0x1C, 0x00}},
};

static uint8_t asciiFallback(uint16_t c) {
  switch (c) {
    case 0x00A0: return ' ';
    case 0x2010: case 0x2011: case 0x2012: case 0x2013: case 0x2014: case 0x2015: case 0x2212:
      return '-';
    case 0x2018: case 0x2019: case 0x201A: case 0x2032: return '\'';
    case 0x201C: case 0x201D: case 0x201E: case 0x2033: return '"';
    case 0x2039: return '<';
    case 0x203A: return '>';
    case 0x2217: case 0x2722: case 0x2733: case 0x2736: case 0x273B: case 0x273D: return '*';
    case 0x2264: return '<';
    case 0x2265: return '>';
    case 0x2260: return '#';
    default: return 0;
  }
}

static inline void px(uint16_t *strip, int x, int y, uint16_t color) {
  if (x >= 0 && x < STRIP_W) strip[y * STRIP_W + x] = color;
}

static void drawColumns(const uint8_t *cols, int x, uint16_t fg, uint16_t *strip) {
  for (int cx = 0; cx < 5; cx++) {
    for (int y = 0; y < STRIP_H; y++) {  // the eighth row holds descenders
      if (cols[cx] & (1 << y)) px(strip, x + cx, y, fg);
    }
  }
}

static void drawBox(uint8_t segs, int x, uint16_t fg, uint16_t *strip) {
  const int cx = x + 2, cy = 3;
  if (segs & SEG_L) for (int i = x; i <= cx; i++) px(strip, i, cy, fg);
  if (segs & SEG_R) for (int i = cx; i < x + CELL_W; i++) px(strip, i, cy, fg);
  if (segs & SEG_U) for (int i = 0; i <= cy; i++) px(strip, cx, i, fg);
  if (segs & SEG_D) for (int i = cy; i < STRIP_H; i++) px(strip, cx, i, fg);
}

static bool drawBlock(uint16_t c, int x, uint16_t fg, uint16_t *strip) {
  if (c < 0x2580 || c > 0x2593) return false;
  for (int y = 0; y < STRIP_H; y++) {
    for (int i = 0; i < CELL_W; i++) {
      bool on = false;
      if (c == 0x2580) on = y < 4;
      else if (c >= 0x2581 && c <= 0x2588) on = y >= STRIP_H - (c - 0x2580);
      else if (c >= 0x2589 && c <= 0x258F) on = i < (CELL_W * (0x2590 - c) + 7) / 8;
      else if (c == 0x2590) on = i >= CELL_W / 2;
      else if (c == 0x2591) on = (i % 2 == 0) && (y % 2 == 0);
      else if (c == 0x2592) on = (i + y) % 2 == 0;
      else if (c == 0x2593) on = !((i % 2 == 0) && (y % 2 == 0));
      if (on) px(strip, x + i, y, fg);
    }
  }
  return true;
}

// Braille is what spinners are made of.
static void drawBraille(uint16_t c, int x, uint16_t fg, uint16_t *strip) {
  static const uint8_t dotX[8] = {1, 1, 1, 4, 4, 4, 1, 4};
  static const uint8_t dotY[8] = {0, 2, 4, 0, 2, 4, 6, 6};
  const uint8_t bits = c - 0x2800;
  for (int d = 0; d < 8; d++) {
    if (bits & (1 << d)) px(strip, x + dotX[d], dotY[d], fg);
  }
}

static void drawGlyph(uint16_t c, int x, uint16_t fg, uint16_t *strip) {
  if (c == ' ') return;
  if (c < 0x7F) {
    drawColumns(&kFont5x7[c * 5], x, fg, strip);
    return;
  }
  if (const uint8_t segs = boxSegments(c)) {
    drawBox(segs, x, fg, strip);
    return;
  }
  if (drawBlock(c, x, fg, strip)) return;
  if (c >= 0x2800 && c <= 0x28FF) {
    drawBraille(c, x, fg, strip);
    return;
  }
  for (const Symbol &s : kSymbols) {
    if (s.cp == c) {
      drawColumns(s.columns, x, fg, strip);
      return;
    }
  }
  if (const uint8_t a = asciiFallback(c)) {
    drawColumns(&kFont5x7[a * 5], x, fg, strip);
    return;
  }
  drawColumns(&kFont5x7['?' * 5], x, fg, strip);
}

void renderRow(const Cell *row, int cursorCol, uint16_t *strip) {
  // The two spare columns of pixels take the colour of the cells beside them.
  for (int y = 0; y < STRIP_H; y++) {
    strip[y * STRIP_W] = row[0].bg;
    strip[y * STRIP_W + STRIP_W - 1] = row[GRID_COLS - 1].bg;
  }
  for (int col = 0; col < GRID_COLS; col++) {
    const Cell &cell = row[col];
    const bool cursor = col == cursorCol;
    const uint16_t fg = cursor ? cell.bg : cell.fg;
    const uint16_t bg = cursor ? VT_CARET : cell.bg;
    const int x = MARGIN_X + col * CELL_W;
    for (int y = 0; y < STRIP_H; y++) {
      for (int i = 0; i < CELL_W; i++) strip[y * STRIP_W + x + i] = bg;
    }
    if (!(cell.flags & CELL_WIDE_TAIL)) drawGlyph(cell.ch, x, fg, strip);
    if (cell.flags & CELL_UNDERLINE) {
      for (int i = 0; i < CELL_W; i++) strip[7 * STRIP_W + x + i] = fg;
    }
  }
}
