// Runs the firmware on a computer: real sockets, a framebuffer, scripted keys.
#pragma once

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netinet/tcp.h>
#include <stdio.h>
#include <string.h>
#include <sys/random.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

#include <deque>
#include <string>
#include <vector>

#include "../SbxTerminal/src/platform.h"

class HostTransport : public Transport {
 public:
  bool open(const char *host, uint16_t port) override {
    close();
    fd_ = socket(AF_INET, SOCK_STREAM, 0);
    sockaddr_in a{};
    a.sin_family = AF_INET;
    a.sin_port = htons(port);
    inet_pton(AF_INET, host, &a.sin_addr);
    if (connect(fd_, (sockaddr *)&a, sizeof(a)) != 0) {
      close();
      return false;
    }
    int one = 1;
    setsockopt(fd_, IPPROTO_TCP, TCP_NODELAY, &one, sizeof(one));
    fcntl(fd_, F_SETFL, fcntl(fd_, F_GETFL) | O_NONBLOCK);
    return true;
  }
  int recv(uint8_t *buf, size_t len) override {
    const ssize_t n = ::recv(fd_, buf, len, 0);
    if (n > 0) return (int)n;
    if (n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) return 0;
    return -1;
  }
  int send(const uint8_t *buf, size_t len) override {
    const ssize_t n = ::send(fd_, buf, len, MSG_NOSIGNAL);
    if (n >= 0) return (int)n;
    return errno == EAGAIN || errno == EWOULDBLOCK ? 0 : -1;
  }
  void close() override {
    if (fd_ >= 0) ::close(fd_);
    fd_ = -1;
  }

 private:
  int fd_ = -1;
};

class HostPlatform : public Platform {
 public:
  uint16_t fb[240][320] = {};
  std::deque<Input> keys;
  Config saved{};
  bool hasSaved = false;

  uint32_t ms() override {
    timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return (uint32_t)(t.tv_sec * 1000 + t.tv_nsec / 1000000);
  }
  void sleep(uint32_t m) override { usleep(m * 1000); }
  void random(uint8_t *buf, size_t len) override {
    if (getrandom(buf, len, 0) != (ssize_t)len) abort();
  }
  void pushStrip(int y, int height, uint16_t *pixels) override {
    for (int r = 0; r < height; r++) memcpy(fb[y + r], pixels + r * 320, 640);
  }
  void clearScreen(uint16_t color) override {
    for (auto &row : fb) {
      for (uint16_t &px : row) px = color;
    }
  }
  bool poll(Input &out) override {
    feedMic();
    if (keys.empty()) return false;
    out = keys.front();
    keys.pop_front();
    return true;
  }
  Transport &transport() override { return net; }
  int wifiScan(WifiNetwork *out, int max) override {
    snprintf(out[0].ssid, sizeof(out[0].ssid), "Test Network");
    out[0].rssi = -48;
    out[0].open = false;
    return max > 0 ? 1 : 0;
  }
  bool wifiJoin(const char *, const char *) override { return true; }
  bool wifiUp() override { return true; }
  bool loadConfig(Config &out) override {
    if (hasSaved) out = saved;
    return hasSaved;
  }
  bool saveConfig(const Config &in) override {
    saved = in;
    hasSaved = true;
    return true;
  }
  KeyboardState keyboard() override { return KB_CONNECTED; }
  void forgetKeyboards() override {}
  void restart() override {}

  bool micStart(MicStream &stream) override {
    mic = &stream;
    micStartedAt = ms();
    micFed = 0;
    return true;
  }
  void micStop() override {
    feedMic();
    mic = nullptr;
  }

  // Samples as the ADC would give them, played back in real time.
  std::vector<uint16_t> micSamples;

  void press(Key k, uint8_t mods = 0) { keys.push_back(Input{k, mods, 0}); }
  void type(const char *s) {
    for (; *s; s++) keys.push_back(Input{KEY_TEXT, 0, *s});
  }
  void screenshot(const std::string &path) {
    FILE *f = fopen(path.c_str(), "wb");
    fprintf(f, "P6\n320 240\n255\n");
    for (int y = 0; y < 240; y++) {
      for (int x = 0; x < 320; x++) {
        const uint16_t c = fb[y][x];
        const uint8_t rgb[3] = {(uint8_t)((c >> 8 & 0xF8) | c >> 13), (uint8_t)((c >> 3 & 0xFC) | (c >> 9 & 3)),
                                (uint8_t)((c << 3 & 0xF8) | (c >> 2 & 7))};
        fwrite(rgb, 1, 3, f);
      }
    }
    fclose(f);
  }

 private:
  void feedMic() {
    if (!mic) return;
    const size_t due = (size_t)(ms() - micStartedAt) * 16;
    while (micFed < due) {
      mic->push(micFed < micSamples.size() ? micSamples[micFed] : 2048);
      micFed++;
    }
  }
  MicStream *mic = nullptr;
  uint32_t micStartedAt = 0;
  size_t micFed = 0;
  HostTransport net;
};
