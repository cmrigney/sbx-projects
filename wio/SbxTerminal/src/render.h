// Draws a row of terminal cells into a 320x13 RGB565 strip.
#pragma once

#include <stdint.h>

#include "vt.h"

static const int STRIP_W = 320;
static const int STRIP_H = 13;
static const int CELL_W = 7;
// The rows are centred on the 240-pixel screen.
static const int SCREEN_TOP = (240 - GRID_ROWS * STRIP_H) / 2;

// cursorCol is the column to draw the cursor in, or -1.
void renderRow(const Cell *row, int cursorCol, uint16_t *strip);
