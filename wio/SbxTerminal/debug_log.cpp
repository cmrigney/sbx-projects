#include "debug_log.h"

#include <Arduino.h>
#include <Seeed_Arduino_FreeRTOS.h>
#include <malloc.h>
#include <stdarg.h>
#include <unistd.h>

static const size_t RING_SIZE = 6144;
static const size_t WRITE_CHUNK = 48;
static const uint32_t HEARTBEAT_MS = 3000;

static char ring[RING_SIZE];
static size_t head = 0, tail = 0;
static LogHeartbeat heartbeatFn = nullptr;

size_t freeHeap() {
  // What malloc holds free, plus what it hasn't claimed yet from the top of RAM.
  const char *top = (const char *)sbrk(0);
  return mallinfo().fordblks + (size_t)((const char *)__get_MSP() - top);
}

static void pushLine(const char *line, size_t len) {
  taskENTER_CRITICAL();
  for (size_t i = 0; i < len; i++) {
    ring[head] = line[i];
    head = (head + 1) % RING_SIZE;
    if (head == tail) tail = (tail + 1) % RING_SIZE;  // full: lose the oldest
  }
  taskEXIT_CRITICAL();
}

void logf(const char *format, ...) {
  char line[168];
  int n = snprintf(line, sizeof(line) - 2, "[%8lu] ", (unsigned long)millis());
  va_list args;
  va_start(args, format);
  const int body = vsnprintf(line + n, sizeof(line) - 2 - n, format, args);
  va_end(args);
  if (body > 0) n += body;
  if (n > (int)sizeof(line) - 3) n = sizeof(line) - 3;
  line[n++] = '\r';
  line[n++] = '\n';
  pushLine(line, n);
}

static void logTask(void *) {
  uint32_t nextBeat = millis() + HEARTBEAT_MS;
  char chunk[WRITE_CHUNK];
  for (;;) {
    if ((int32_t)(millis() - nextBeat) >= 0) {
      nextBeat = millis() + HEARTBEAT_MS;
      if (heartbeatFn) heartbeatFn();
    }
    if (Serial) {
      size_t n = 0;
      taskENTER_CRITICAL();
      while (n < WRITE_CHUNK && tail != head) {
        chunk[n++] = ring[tail];
        tail = (tail + 1) % RING_SIZE;
      }
      taskEXIT_CRITICAL();
      if (n) Serial.write((const uint8_t *)chunk, n);
    }
    vTaskDelay(pdMS_TO_TICKS(10));
  }
}

void logBegin(LogHeartbeat heartbeat) {
  heartbeatFn = heartbeat;
  // Above the main code, which doesn't yield while drawing, and below the RPC server.
  if (xTaskCreate(logTask, "log", 768, nullptr, configMAX_PRIORITIES - 1, nullptr) != pdPASS) {
    Serial.println("Couldn't start the log task");
  }
}
