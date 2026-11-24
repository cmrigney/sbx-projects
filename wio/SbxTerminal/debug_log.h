// Timestamped logging to USB serial (115200 baud). Lines go through a buffer drained by its own
// task, so logging is safe from BLE callbacks and keeps working while the main code is stuck in
// an RPC call. The newest lines wait in the buffer until a serial monitor is opened.
#pragma once

typedef void (*LogHeartbeat)();

#include <stddef.h>

void logBegin(LogHeartbeat heartbeat);
size_t freeHeap();
void logf(const char *format, ...) __attribute__((format(printf, 1, 2)));
