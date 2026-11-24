// What the firmware needs from the board. The Wio implementation is in ../SbxTerminal.ino; the
// tests in wio/test run the same code on a computer against a real sbx-projects server.
#pragma once

#include <stddef.h>
#include <stdint.h>

#include "mic.h"

enum Key : uint8_t {
  KEY_NONE,
  KEY_TEXT,  // Input::ch holds the character
  KEY_ENTER,
  KEY_ESC,
  KEY_BACKSPACE,
  KEY_TAB,
  KEY_DELETE,
  KEY_INSERT,
  KEY_UP,
  KEY_DOWN,
  KEY_LEFT,
  KEY_RIGHT,
  KEY_HOME,
  KEY_END,
  KEY_PGUP,
  KEY_PGDN,
  KEY_F1,  // through KEY_F1 + 11
  KEY_F12 = KEY_F1 + 11,
  // The 5-way switch and the three top buttons.
  SW_UP,
  SW_DOWN,
  SW_LEFT,
  SW_RIGHT,
  SW_PRESS,
  BTN_A,  // right
  BTN_B,  // middle
  BTN_C,  // left
  BTN_B_HOLD,  // B held down; a short press is BTN_B, sent on release
};

enum : uint8_t { MOD_CTRL = 0x11, MOD_SHIFT = 0x22, MOD_ALT = 0x44, MOD_GUI = 0x88 };

struct Input {
  Key key;
  uint8_t mods;
  char ch;
};

// A byte stream to the server. recv never blocks: it returns 0 when nothing has arrived and a
// negative number once the connection is gone.
class Transport {
 public:
  virtual ~Transport() {}
  virtual bool open(const char *host, uint16_t port) = 0;
  virtual int recv(uint8_t *buf, size_t len) = 0;
  virtual int send(const uint8_t *buf, size_t len) = 0;
  virtual void close() = 0;
};

// Saved across power cycles and sketch uploads.
struct Config {
  char ssid[33];
  char pass[64];
  char host[64];
  uint16_t port;
  uint8_t fingerprint[32];
  char token[65];
  bool paired;
  uint8_t fontSize;  // added later: settings saved before it have none
};

struct WifiNetwork {
  char ssid[33];
  int8_t rssi;
  bool open;
};

enum KeyboardState : uint8_t { KB_NONE, KB_CONNECTED, KB_LOST };

class Platform {
 public:
  virtual ~Platform() {}
  virtual uint32_t ms() = 0;
  virtual void sleep(uint32_t ms) = 0;
  virtual void random(uint8_t *buf, size_t len) = 0;

  // Draws a 320-pixel-wide strip at pixel row y. It may change the buffer.
  virtual void pushStrip(int y, int height, uint16_t *pixels) = 0;
  virtual void clearScreen(uint16_t color) = 0;
  virtual bool poll(Input &out) = 0;

  virtual Transport &transport() = 0;
  virtual int wifiScan(WifiNetwork *out, int max) = 0;
  virtual bool wifiJoin(const char *ssid, const char *pass) = 0;
  virtual bool wifiUp() = 0;

  virtual bool loadConfig(Config &out) = 0;
  virtual bool saveConfig(const Config &in) = 0;

  virtual KeyboardState keyboard() = 0;
  virtual void forgetKeyboards() = 0;
  virtual void restart() = 0;

  // Starts feeding the microphone into stream at 16 kHz, from an interrupt.
  virtual bool micStart(MicStream &stream) = 0;
  virtual void micStop() = 0;
};
