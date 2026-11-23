// Draws a row of terminal cells into a 320x8 RGB565 strip.
#pragma once

#include <stdint.h>

#include "vt.h"

static const int STRIP_W = 320;
static const int STRIP_H = 8;

// cursorCol is the column to draw the cursor in, or -1.
void renderRow(const Cell *row, int cursorCol, uint16_t *strip);
