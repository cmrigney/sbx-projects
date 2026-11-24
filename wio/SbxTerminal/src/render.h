// Draws a row of terminal cells into a 320-pixel-wide RGB565 strip, in the current font.
#pragma once

#include <stdint.h>

#include "vt.h"

static const int STRIP_W = 320;
static const int STRIP_MAX_H = 20;

extern int cellW, stripH, screenTop;

int fontCount();
const char *fontName(int index);
// Selects a font and sets the grid's size to what fits the screen.
void setFont(int index);

// cursorCol is the column to draw the cursor in, or -1.
void renderRow(const Cell *row, int cursorCol, uint16_t *strip);
