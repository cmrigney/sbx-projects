// HTTP/1.1 and WebSocket clients over PinnedTls. One connection is open at a time.
#pragma once

#include "pinned_tls.h"

class Http {
 public:
  Http(Platform &platform, PinnedTls &tls) : p_(platform), tls_(tls) {}

  // Sends the request and reads the response's headers. Returns the HTTP status, or 0 with
  // error() set. Call end() when done with the body.
  int request(const Config &config, const char *method, const char *path, const char *body,
              bool withToken = true);
  void end();

  // The response's body, de-chunked; the shape ArduinoJson reads from.
  int read();
  size_t readBytes(char *out, size_t len);

  const char *error() const { return error_; }

 private:
  int rawByte();
  bool readLine(char *out, size_t cap);

  Platform &p_;
  PinnedTls &tls_;
  uint8_t buf_[512];
  size_t pos_ = 0, len_ = 0;
  bool chunked_ = false, ended_ = false;
  int32_t remaining_ = -1;  // bytes left in the body or chunk; -1 for "until the socket closes"
  const char *error_ = "";
};

class WebSocket {
 public:
  WebSocket(Platform &platform, PinnedTls &tls) : p_(platform), tls_(tls) {}

  bool open(const Config &config, const char *path);
  // Copies terminal output into out. Returns the bytes copied, 0 for none, -1 once closed.
  int poll(uint8_t *out, size_t max);
  bool sendBinary(const uint8_t *data, size_t len);
  // A JSON message, as one text frame.
  bool sendText(const char *json);
  // A dictate-audio message. len is at most MAX_AUDIO_CHUNK.
  bool sendAudio(const uint8_t *data, size_t len);
  // The JSON message poll stopped at, if any. It stays valid until the next poll after clearText.
  char *text() { return textReady_ ? text_ : nullptr; }
  void clearText() { textReady_ = false; }
  void close();

  static const size_t MAX_AUDIO_CHUNK = 480;

  uint16_t closeCode() const { return closeCode_; }
  const char *closeReason() const { return closeReason_; }
  const char *error() const { return error_; }

 private:
  enum State : uint8_t { HEAD, LEN, LEN_EXT, PAYLOAD, CONTROL };

  bool sendFrame(uint8_t opcode, const uint8_t *data, size_t len);
  void finishControl();

  Platform &p_;
  PinnedTls &tls_;
  uint8_t rx_[1024];
  size_t rxPos_ = 0, rxLen_ = 0;

  State state_ = HEAD;
  uint8_t opcode_ = 0;
  uint8_t lenBytes_ = 0;
  uint64_t lenValue_ = 0;
  uint64_t left_ = 0;
  uint8_t control_[125];
  uint8_t controlLen_ = 0;

  char text_[1280];  // a transcript of up to 1000 bytes, some of them escaped
  size_t textLen_ = 0;
  bool textTooLong_ = false, textReady_ = false;

  bool open_ = false;
  uint16_t closeCode_ = 0;
  char closeReason_[124] = "";
  const char *error_ = "";
};
