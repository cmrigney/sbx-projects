// Reads a Bluetooth LE advertisement without allocating, so a busy room can't exhaust the heap.
#pragma once

#include <stddef.h>
#include <stdint.h>
#include <string.h>

static const uint16_t ADV_UUID_HID = 0x1812;
static const uint16_t ADV_APPEARANCE_KEYBOARD = 0x03C1;

struct AdvInfo {
  char name[32];
  bool hid;
  uint16_t appearance;
};

inline void parseAdv(const uint8_t *data, size_t len, AdvInfo &out) {
  memset(&out, 0, sizeof(out));
  for (size_t pos = 0; pos < len;) {
    const size_t fieldLen = data[pos];
    if (fieldLen == 0 || pos + 1 + fieldLen > len) break;
    const uint8_t type = data[pos + 1];
    const uint8_t *value = data + pos + 2;
    const size_t valueLen = fieldLen - 1;
    if (type == 0x02 || type == 0x03) {  // 16-bit service UUIDs
      for (size_t i = 0; i + 1 < valueLen; i += 2) {
        if ((uint16_t)(value[i] | value[i + 1] << 8) == ADV_UUID_HID) out.hid = true;
      }
    } else if (type == 0x08 || type == 0x09) {  // shortened or complete name
      const size_t n = valueLen < sizeof(out.name) - 1 ? valueLen : sizeof(out.name) - 1;
      memcpy(out.name, value, n);
      out.name[n] = 0;
    } else if (type == 0x19 && valueLen >= 2) {  // appearance
      out.appearance = value[0] | value[1] << 8;
    }
    pos += 1 + fieldLen;
  }
}

// Many keyboards advertise only their name, without the HID service or appearance.
inline bool advIsKeyboard(const AdvInfo &info) {
  if (info.hid || info.appearance == ADV_APPEARANCE_KEYBOARD) return true;
  static const char needle[] = "keyboard";
  const size_t needleLen = sizeof(needle) - 1;
  for (const char *s = info.name; *s; s++) {
    size_t i = 0;
    while (i < needleLen && s[i] && (s[i] | 0x20) == needle[i]) i++;
    if (i == needleLen) return true;
  }
  return false;
}
