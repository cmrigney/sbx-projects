// A Bluetooth Low Energy keyboard, from wio-terminal-playground/BleKeyboard.
//
// Needs RTL8720 firmware 2.1.3-ftl: stock firmware fails every bond, and keyboards then send
// nothing. Classic-only (BR/EDR) keyboards can't work, as the RTL8720 firmware only exposes BLE.
#pragma once

#include "src/platform.h"

namespace BleKeyboard {

typedef void (*StatusFn)(const char *title, const char *detail);
typedef bool (*CancelFn)();

// Starts Bluetooth. Returns false when the RTL8720 firmware can't store bonds.
bool begin();
void clearBonds();

// Whether the last restart was the watchdog ending a connection attempt that hung.
bool hungLastBoot();
// Describes where that attempt hung.
void hangReport(char *out, size_t cap);
// Logs the current step while a connection attempt is under way.
void logProgress();

// Scans until a keyboard is found or cancel returns true, then pairs and subscribes. rpcBLE
// allows one client per boot, so connecting again after a failure needs a restart.
bool connect(StatusFn status, CancelFn cancel);

bool poll(Input &out);
KeyboardState state();

}  // namespace BleKeyboard
