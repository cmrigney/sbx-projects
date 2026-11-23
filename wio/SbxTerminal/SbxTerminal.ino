// sbx-projects on a Wio Terminal: browse projects and sandboxes, make new ones, and attach to
// them in a terminal, typing on a Bluetooth keyboard. See ../README.md.

#include <rpcWiFi.h>
#include <TFT_eSPI.h>
#include <sfud.h>
#include <ArduinoBearSSL.h>  // for its copy of BearSSL
#include <ArduinoJson.h>

#include "ble_keyboard.h"
#include "debug_log.h"
#include "src/app.h"

// ---------------------------------------------------------------------------
// Network
// ---------------------------------------------------------------------------

class WioTransport : public Transport {
 public:
  bool open(const char *host, uint16_t port) override {
    client_.stop();
    logf("net: connecting to %s:%u", host, port);
    if (!client_.connect(host, port)) {
      logf("net: connection failed");
      return false;
    }
    client_.setNoDelay(true);
    return true;
  }
  int recv(uint8_t *buf, size_t len) override {
    const int available = client_.available();
    if (available > 0) {
      const int n = client_.read(buf, len < (size_t)available ? len : (size_t)available);
      return n > 0 ? n : 0;
    }
    return client_.connected() ? 0 : -1;
  }
  int send(const uint8_t *buf, size_t len) override {
    const size_t n = client_.write(buf, len);
    if (n > 0) return (int)n;
    return client_.connected() ? 0 : -1;
  }
  void close() override { client_.stop(); }

 private:
  WiFiClient client_;
};

// ---------------------------------------------------------------------------
// Settings, in the last sector of the QSPI flash
// ---------------------------------------------------------------------------

static const uint32_t CONFIG_MAGIC = 0x53425831;  // "SBX1"
static const size_t SECTOR_SIZE = 4096;

struct StoredConfig {
  uint32_t magic;
  uint32_t size;
  Config config;
  uint32_t crc;
};

static uint32_t crc32(const uint8_t *data, size_t len) {
  uint32_t crc = 0xFFFFFFFFUL;
  for (size_t i = 0; i < len; i++) {
    crc ^= data[i];
    for (int b = 0; b < 8; b++) crc = (crc >> 1) ^ (0xEDB88320UL & (0 - (crc & 1)));
  }
  return ~crc;
}

static const sfud_flash *settingsFlash() {
  static bool tried = false;
  static const sfud_flash *flash = nullptr;
  if (!tried) {
    tried = true;
    const sfud_err err = sfud_init();
    if (err == SFUD_SUCCESS) {
      sfud_flash *device = sfud_get_device(SFUD_W25Q32_DEVICE_INDEX);
      // Required: in the default mode this port's sfud_read reports success without reading.
      sfud_qspi_fast_read_enable(device, 2);
      flash = device;
      logf("flash: ready, %lu bytes", (unsigned long)flash->chip.capacity);
    } else {
      logf("flash: init failed, error %d", err);
    }
  }
  return flash;
}

static bool readStored(const sfud_flash *flash, StoredConfig &s) {
  if (sfud_read(flash, flash->chip.capacity - SECTOR_SIZE, sizeof(s), (uint8_t *)&s) != SFUD_SUCCESS) {
    return false;
  }
  return s.magic == CONFIG_MAGIC && s.size == sizeof(Config) &&
         s.crc == crc32((const uint8_t *)&s.config, sizeof(Config));
}

// The sector is only ever written when it's blank or already ours, so data another sketch keeps
// on the flash isn't overwritten.
static bool sectorIsOurs(const sfud_flash *flash) {
  static uint8_t sector[SECTOR_SIZE];
  const sfud_err err = sfud_read(flash, flash->chip.capacity - SECTOR_SIZE, SECTOR_SIZE, sector);
  if (err != SFUD_SUCCESS) {
    logf("flash: read failed, error %d", err);
    return false;
  }
  if (((StoredConfig *)sector)->magic == CONFIG_MAGIC) return true;
  size_t used = 0;
  for (size_t i = 0; i < SECTOR_SIZE; i++) used += sector[i] != 0xFF;
  if (used) logf("flash: last sector holds other data (%u non-blank bytes), not writing", (unsigned)used);
  return used == 0;
}

// ---------------------------------------------------------------------------
// Board
// ---------------------------------------------------------------------------

TFT_eSPI tft;

struct Button {
  uint32_t pin;
  Key key;
  bool repeats;
  bool down;
  uint32_t changedAt, repeatAt;
};

static Button buttons[] = {
    {WIO_5S_UP, SW_UP, true, false, 0, 0},       {WIO_5S_DOWN, SW_DOWN, true, false, 0, 0},
    {WIO_5S_LEFT, SW_LEFT, true, false, 0, 0},   {WIO_5S_RIGHT, SW_RIGHT, true, false, 0, 0},
    {WIO_5S_PRESS, SW_PRESS, false, false, 0, 0}, {WIO_KEY_A, BTN_A, false, false, 0, 0},
    {WIO_KEY_B, BTN_B, false, false, 0, 0},       {WIO_KEY_C, BTN_C, false, false, 0, 0},
};

class WioPlatform : public Platform {
 public:
  uint32_t ms() override { return millis(); }
  void sleep(uint32_t m) override { delay(m); }

  void random(uint8_t *buf, size_t len) override {
    MCLK->APBCMASK.bit.TRNG_ = 1;
    TRNG->CTRLA.bit.ENABLE = 1;
    for (size_t i = 0; i < len;) {
      while (!TRNG->INTFLAG.bit.DATARDY) {
      }
      uint32_t v = TRNG->DATA.reg;
      for (int b = 0; b < 4 && i < len; b++, i++, v >>= 8) buf[i] = (uint8_t)v;
    }
  }

  void pushStrip(int y, uint16_t *pixels) override {
    // The panel takes the high byte first. On the SAMD51 this library sends memory as it is and
    // ignores setSwapBytes, so the bytes are swapped here.
    for (int i = 0; i < STRIP_W * STRIP_H; i++) pixels[i] = __builtin_bswap16(pixels[i]);
    tft.pushImage(0, y, STRIP_W, STRIP_H, pixels);
  }

  bool poll(Input &out) override {
    const uint32_t now = millis();
    for (Button &b : buttons) {
      const bool down = digitalRead(b.pin) == LOW;
      if (down != b.down && now - b.changedAt >= 25) {
        b.down = down;
        b.changedAt = now;
        if (down) {
          b.repeatAt = now + 350;
          out = Input{b.key, 0, 0};
          return true;
        }
      } else if (down && b.down && b.repeats && (int32_t)(now - b.repeatAt) >= 0) {
        b.repeatAt = now + 110;
        out = Input{b.key, 0, 0};
        return true;
      }
    }
    return BleKeyboard::poll(out);
  }

  Transport &transport() override { return transport_; }

  int wifiScan(WifiNetwork *out, int max) override {
    logf("wifi: scanning");
    const int found = WiFi.scanNetworks();
    logf("wifi: scan found %d, keyboard state %d", found, BleKeyboard::state());
    int n = 0;
    for (int i = 0; i < found && n < max; i++) {
      const String ssid = WiFi.SSID(i);
      if (!ssid.length()) continue;
      bool duplicate = false;
      for (int j = 0; j < n; j++) duplicate |= ssid == out[j].ssid;
      if (duplicate) continue;
      snprintf(out[n].ssid, sizeof(out[n].ssid), "%s", ssid.c_str());
      out[n].rssi = (int8_t)WiFi.RSSI(i);
      out[n].open = WiFi.encryptionType(i) == WIFI_AUTH_OPEN;
      n++;
    }
    WiFi.scanDelete();
    return n;
  }

  bool wifiJoin(const char *ssid, const char *pass) override {
    logf("wifi: joining %s", ssid);
    WiFi.disconnect();
    delay(100);
    WiFi.begin(ssid, pass[0] ? pass : nullptr);
    const uint32_t start = millis();
    while (WiFi.status() != WL_CONNECTED && millis() - start < 20000) delay(100);
    logf("wifi: status %d, address %s, keyboard state %d", WiFi.status(),
         WiFi.localIP().toString().c_str(), BleKeyboard::state());
    return WiFi.status() == WL_CONNECTED;
  }

  bool wifiUp() override { return WiFi.status() == WL_CONNECTED; }

  bool loadConfig(Config &out) override {
    const sfud_flash *flash = settingsFlash();
    static StoredConfig s;
    if (!flash || !readStored(flash, s)) {
      logf("flash: no saved settings");
      return false;
    }
    out = s.config;
    logf("flash: loaded settings, paired %d", out.paired);
    return true;
  }

  bool saveConfig(const Config &in) override {
    const sfud_flash *flash = settingsFlash();
    if (!flash || !sectorIsOurs(flash)) return false;
    static StoredConfig s;
    memset(&s, 0, sizeof(s));
    s.magic = CONFIG_MAGIC;
    s.size = sizeof(Config);
    s.config = in;
    s.crc = crc32((const uint8_t *)&s.config, sizeof(Config));
    const sfud_err err = sfud_erase_write(flash, flash->chip.capacity - SECTOR_SIZE, sizeof(s), (const uint8_t *)&s);
    if (err != SFUD_SUCCESS) {
      logf("flash: write failed, error %d", err);
      return false;
    }
    static StoredConfig check;
    const bool ok = readStored(flash, check) && memcmp(&check.config, &s.config, sizeof(Config)) == 0;
    logf("flash: settings %s", ok ? "saved" : "didn't read back correctly");
    return ok;
  }

  KeyboardState keyboard() override { return BleKeyboard::state(); }
  void forgetKeyboards() override { BleKeyboard::clearBonds(); }
  void restart() override { NVIC_SystemReset(); }

 private:
  WioTransport transport_;
};

static WioPlatform platform;
static App app(platform);

// ---------------------------------------------------------------------------

static void bootScreen(const char *title, const char *detail) {
  tft.fillScreen(VT_DEFAULT_BG);
  tft.setTextColor(VT_CARET, VT_DEFAULT_BG);
  tft.setTextDatum(MC_DATUM);
  tft.drawString(title, 160, 100, 4);
  tft.setTextColor(VT_DEFAULT_FG, VT_DEFAULT_BG);
  if (detail) tft.drawString(detail, 160, 136, 2);
}

static bool skipKeyboard() { return digitalRead(WIO_KEY_C) == LOW; }

void setup() {
  Serial.begin(115200);
  // USB serial re-enumerates after reset; give the monitor a moment, but still run on battery.
  while (!Serial && millis() < 2000) delay(10);
  logBegin(BleKeyboard::logProgress);
  logf("boot: sbx terminal, reset cause 0x%02x", RSTC->RCAUSE.reg);

  for (const Button &b : buttons) pinMode(b.pin, INPUT_PULLUP);
  tft.begin();
  tft.setRotation(3);
  bootScreen("sbx-projects", "Starting Bluetooth");

  // Before Bluetooth starts: changing the Wi-Fi mode restarts the radio they share, which would
  // drop a connected keyboard. With the mode set here, later scans and joins don't change it.
  logf("boot: setting Wi-Fi mode");
  WiFi.mode(WIFI_STA);
  logf("boot: starting Bluetooth");

  const bool canBond = BleKeyboard::begin();
  if (!canBond) {
    bootScreen("Old RTL8720 firmware", "Keyboards need 2.1.3-ftl to pair");
    delay(4000);
  }
  if (skipKeyboard()) {
    logf("boot: forgetting keyboards");
    BleKeyboard::clearBonds();
    bootScreen("Keyboards forgotten", "Release C, then put it in pairing mode");
    while (skipKeyboard()) delay(10);
    delay(300);
  }
  if (BleKeyboard::hungLastBoot()) {
    char report[64];
    BleKeyboard::hangReport(report, sizeof(report));
    logf("boot: last keyboard connection hung at %s", report);
    bootScreen("Keyboard hung", report);
    tft.drawString("Press the 5-way to continue", 160, 170, 2);
    while (digitalRead(WIO_5S_PRESS) != LOW) delay(10);
    while (digitalRead(WIO_5S_PRESS) == LOW) delay(10);
  } else if (!BleKeyboard::connect(bootScreen, skipKeyboard)) {
    if (!skipKeyboard()) {
      bootScreen("No keyboard", "Continuing. Restart to try again.");
      delay(2000);
    }
    while (skipKeyboard()) delay(10);  // so the held button doesn't act in the app
  }
  tft.fillScreen(VT_DEFAULT_BG);
  app.begin();
}

void loop() { app.tick(); }
