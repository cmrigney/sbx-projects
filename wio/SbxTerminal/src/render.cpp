#include "render.h"

#include "fonts.h"

static const char *const kFontNames[] = {"Small", "Medium", "Large"};
static const FontData *font = &kFonts[0];

int cellW = 7, stripH = 13, screenTop = 3;
static int marginX = 2;

int fontCount() { return sizeof(kFonts) / sizeof(kFonts[0]); }

const char *fontName(int index) { return kFontNames[index]; }

void setFont(int index) {
  font = &kFonts[index < 0 || index >= fontCount() ? 0 : index];
  cellW = font->w;
  stripH = font->h;
  gridCols = STRIP_W / cellW;
  gridRows = 240 / stripH;
  termRows = gridRows - 1;
  marginX = (STRIP_W - gridCols * cellW) / 2;
  screenTop = (240 - gridRows * stripH) / 2;
}

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

static inline void px(uint16_t *strip, int x, int y, uint16_t color) {
  if (x >= 0 && x < STRIP_W && y >= 0 && y < stripH) strip[y * STRIP_W + x] = color;
}

static const uint8_t *findGlyph(uint16_t c) {
  int low = 0, high = font->count - 1;
  while (low <= high) {
    const int mid = (low + high) / 2;
    if (font->codes[mid] == c) return font->bitmaps + mid * font->rowBytes * font->h;
    if (font->codes[mid] < c) low = mid + 1;
    else high = mid - 1;
  }
  return nullptr;
}

static void drawBitmap(const uint8_t *rows, int x, uint16_t fg, bool bold, uint16_t *strip) {
  const bool wide = font->rowBytes == 2;
  for (int y = 0; y < stripH; y++) {
    uint16_t bits = wide ? (uint16_t)(rows[y * 2] << 8 | rows[y * 2 + 1]) : (uint16_t)(rows[y] << 8);
    if (bold) bits |= bits >> 1;  // each font's cell keeps its last column free for this
    for (int i = 0; i < cellW; i++) {
      if (bits & (0x8000 >> i)) px(strip, x + i, y, fg);
    }
  }
}

static void drawBox(uint8_t segs, int x, uint16_t fg, bool bold, uint16_t *strip) {
  const int cx = x + cellW / 2;
  const int cy = stripH / 2;
  const auto horizontal = [&](int from, int to) {
    for (int i = from; i <= to; i++) {
      px(strip, i, cy, fg);
      if (bold) px(strip, i, cy + 1, fg);
    }
  };
  const auto vertical = [&](int from, int to) {
    for (int i = from; i <= to; i++) {
      px(strip, cx, i, fg);
      if (bold) px(strip, cx + 1, i, fg);
    }
  };
  if (segs & SEG_L) horizontal(x, cx);
  if (segs & SEG_R) horizontal(cx, x + cellW - 1);
  if (segs & SEG_U) vertical(0, cy);
  if (segs & SEG_D) vertical(cy, stripH - 1);
}

static bool drawBlock(uint16_t c, int x, uint16_t fg, uint16_t *strip) {
  if (c < 0x2580 || c > 0x2593) return false;
  for (int y = 0; y < stripH; y++) {
    for (int i = 0; i < cellW; i++) {
      bool on = false;
      if (c == 0x2580) on = y < stripH / 2;
      else if (c >= 0x2581 && c <= 0x2588) on = y >= stripH - (stripH * (c - 0x2580) + 4) / 8;
      else if (c >= 0x2589 && c <= 0x258F) on = i < (cellW * (0x2590 - c) + 4) / 8;
      else if (c == 0x2590) on = i >= cellW / 2;
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
  static const uint8_t column[8] = {0, 0, 0, 1, 1, 1, 0, 1};
  static const uint8_t line[8] = {0, 1, 2, 0, 1, 2, 3, 3};
  const uint8_t bits = c - 0x2800;
  for (int d = 0; d < 8; d++) {
    if (!(bits & (1 << d))) continue;
    const int dx = x + cellW * (1 + 2 * column[d]) / 5;
    const int dy = stripH * (1 + 3 * line[d]) / 13;
    px(strip, dx, dy, fg);
    px(strip, dx + 1, dy, fg);
    px(strip, dx, dy + 1, fg);
    px(strip, dx + 1, dy + 1, fg);
  }
}

static void drawGlyph(uint16_t c, int x, uint16_t fg, bool bold, uint16_t *strip) {
  if (c == ' ') return;
  if (const uint8_t segs = boxSegments(c)) {
    drawBox(segs, x, fg, bold, strip);
    return;
  }
  if (drawBlock(c, x, fg, strip)) return;
  if (c >= 0x2800 && c <= 0x28FF) {
    drawBraille(c, x, fg, strip);
    return;
  }
  const uint8_t *glyph = findGlyph(c);
  if (!glyph) glyph = findGlyph('?');
  drawBitmap(glyph, x, fg, bold, strip);
}

void renderRow(const Cell *row, int cursorCol, uint16_t *strip) {
  // The spare columns of pixels take the colour of the cells beside them.
  for (int y = 0; y < stripH; y++) {
    for (int x = 0; x < marginX; x++) strip[y * STRIP_W + x] = row[0].bg;
    for (int x = marginX + gridCols * cellW; x < STRIP_W; x++) strip[y * STRIP_W + x] = row[gridCols - 1].bg;
  }
  for (int col = 0; col < gridCols; col++) {
    const Cell &cell = row[col];
    const bool cursor = col == cursorCol;
    const uint16_t fg = cursor ? cell.bg : cell.fg;
    const uint16_t bg = cursor ? VT_CARET : cell.bg;
    const int x = marginX + col * cellW;
    for (int y = 0; y < stripH; y++) {
      for (int i = 0; i < cellW; i++) strip[y * STRIP_W + x + i] = bg;
    }
    if (!(cell.flags & CELL_WIDE_TAIL)) drawGlyph(cell.ch, x, fg, cell.flags & CELL_BOLD, strip);
    if (cell.flags & CELL_UNDERLINE) {
      for (int i = 0; i < cellW; i++) strip[(stripH - 1) * STRIP_W + x + i] = fg;
    }
  }
}
