#include "net.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const uint32_t RESPONSE_MS = 35000;  // /v1/options can wait 20 s for sbx

static bool startsWithNoCase(const char *s, const char *prefix) {
  for (; *prefix; s++, prefix++) {
    char a = *s, b = *prefix;
    if (a >= 'A' && a <= 'Z') a += 32;
    if (b >= 'A' && b <= 'Z') b += 32;
    if (a != b) return false;
  }
  return true;
}

static bool sendRequestHead(PinnedTls &tls, const Config &c, const char *method, const char *path,
                            const char *extra, size_t bodyLen, bool withToken) {
  char head[512];
  int n = snprintf(head, sizeof(head), "%s %s HTTP/1.1\r\nHost: %s:%u\r\n", method, path, c.host,
                   (unsigned)c.port);
  if (n <= 0 || n >= (int)sizeof(head)) return false;
  if (withToken) {
    n += snprintf(head + n, sizeof(head) - n, "Authorization: Bearer %s\r\n", c.token);
  }
  if (extra) n += snprintf(head + n, sizeof(head) - n, "%s", extra);
  if (bodyLen) {
    n += snprintf(head + n, sizeof(head) - n,
                  "Content-Type: application/json\r\nContent-Length: %u\r\n", (unsigned)bodyLen);
  }
  if (n >= (int)sizeof(head) - 3) return false;
  n += snprintf(head + n, sizeof(head) - n, "\r\n");
  const bool ok = tls.write((const uint8_t *)head, n);
  memset(head, 0, sizeof(head));  // it held the token
  return ok;
}

// ---------------------------------------------------------------------------
// HTTP
// ---------------------------------------------------------------------------

int Http::rawByte() {
  if (pos_ < len_) return buf_[pos_++];
  const uint32_t start = p_.ms();
  for (;;) {
    const int n = tls_.read(buf_, sizeof(buf_));
    if (n > 0) {
      pos_ = 1;
      len_ = n;
      return buf_[0];
    }
    if (n < 0) return -1;
    if (p_.ms() - start > RESPONSE_MS) {
      error_ = "The server didn't answer";
      return -1;
    }
    p_.sleep(2);
  }
}

bool Http::readLine(char *out, size_t cap) {
  size_t n = 0;
  for (;;) {
    const int c = rawByte();
    if (c < 0) return false;
    if (c == '\n') break;
    if (c != '\r' && n + 1 < cap) out[n++] = (char)c;
  }
  out[n] = 0;
  return true;
}

int Http::request(const Config &c, const char *method, const char *path, const char *body,
                  bool withToken) {
  error_ = "";
  pos_ = len_ = 0;
  chunked_ = ended_ = false;
  remaining_ = -1;
  if (!tls_.connect(c.host, c.port, c.fingerprint)) {
    error_ = tls_.error();
    return 0;
  }
  const size_t bodyLen = body ? strlen(body) : 0;
  if (!sendRequestHead(tls_, c, method, path, "Connection: close\r\n", bodyLen, withToken) ||
      (bodyLen && !tls_.write((const uint8_t *)body, bodyLen))) {
    error_ = "Lost the connection";
    tls_.close();
    return 0;
  }

  char line[128];
  if (!readLine(line, sizeof(line)) || strncmp(line, "HTTP/1.", 7) != 0) {
    if (!error_[0]) error_ = "The server sent a bad response";
    tls_.close();
    return 0;
  }
  const int status = atoi(line + 9);
  for (;;) {
    if (!readLine(line, sizeof(line))) {
      if (!error_[0]) error_ = "Lost the connection";
      tls_.close();
      return 0;
    }
    if (!line[0]) break;
    if (startsWithNoCase(line, "content-length:")) remaining_ = atol(line + 15);
    else if (startsWithNoCase(line, "transfer-encoding:") && strstr(line, "chunked")) chunked_ = true;
  }
  if (chunked_) remaining_ = 0;
  return status;
}

int Http::read() {
  if (ended_) return -1;
  if (chunked_ && remaining_ == 0) {
    char line[24];
    if (!readLine(line, sizeof(line))) return -1;
    if (!line[0] && !readLine(line, sizeof(line))) return -1;  // the CRLF after a chunk
    remaining_ = strtol(line, nullptr, 16);
    if (remaining_ <= 0) {
      ended_ = true;
      return -1;
    }
  } else if (!chunked_ && remaining_ == 0) {
    ended_ = true;
    return -1;
  }
  const int c = rawByte();
  if (c < 0) {
    ended_ = true;
    return -1;
  }
  if (remaining_ > 0) remaining_--;
  return c;
}

size_t Http::readBytes(char *out, size_t len) {
  size_t n = 0;
  while (n < len) {
    const int c = read();
    if (c < 0) break;
    out[n++] = (char)c;
  }
  return n;
}

void Http::end() { tls_.close(); }

// ---------------------------------------------------------------------------
// WebSocket
// ---------------------------------------------------------------------------

static void base64(const uint8_t *in, size_t len, char *out) {
  static const char alphabet[] = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  size_t o = 0;
  for (size_t i = 0; i < len; i += 3) {
    const uint32_t v = in[i] << 16 | (i + 1 < len ? in[i + 1] << 8 : 0) | (i + 2 < len ? in[i + 2] : 0);
    out[o++] = alphabet[(v >> 18) & 63];
    out[o++] = alphabet[(v >> 12) & 63];
    out[o++] = i + 1 < len ? alphabet[(v >> 6) & 63] : '=';
    out[o++] = i + 2 < len ? alphabet[v & 63] : '=';
  }
  out[o] = 0;
}

bool WebSocket::open(const Config &c, const char *path) {
  close();
  error_ = "";
  closeCode_ = 0;
  closeReason_[0] = 0;
  state_ = HEAD;
  rxPos_ = rxLen_ = 0;
  textLen_ = 0;
  textTooLong_ = textReady_ = false;
  if (!tls_.connect(c.host, c.port, c.fingerprint)) {
    error_ = tls_.error();
    return false;
  }

  uint8_t nonce[16];
  p_.random(nonce, sizeof(nonce));
  char key[25];
  base64(nonce, sizeof(nonce), key);
  char extra[160];
  snprintf(extra, sizeof(extra),
           "Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\n"
           "Sec-WebSocket-Key: %s\r\nSec-WebSocket-Protocol: sbxp-terminal\r\n", key);
  if (!sendRequestHead(tls_, c, "GET", path, extra, 0, true)) {
    error_ = "Lost the connection";
    tls_.close();
    return false;
  }

  br_sha1_context sha;
  br_sha1_init(&sha);
  br_sha1_update(&sha, key, strlen(key));
  static const char guid[] = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11";
  br_sha1_update(&sha, guid, sizeof(guid) - 1);
  uint8_t digest[20];
  br_sha1_out(&sha, digest);
  char expected[29];
  base64(digest, sizeof(digest), expected);

  // Headers, byte by byte out of rx_, so frames that follow them stay buffered.
  char line[160];
  size_t n = 0;
  int status = 0;
  bool first = true, accepted = false;
  const uint32_t start = p_.ms();
  for (;;) {
    if (rxPos_ == rxLen_) {
      const int r = tls_.read(rx_, sizeof(rx_));
      if (r < 0) {
        error_ = "Lost the connection";
        tls_.close();
        return false;
      }
      if (r == 0) {
        if (p_.ms() - start > RESPONSE_MS) {
          error_ = "The server didn't answer";
          tls_.close();
          return false;
        }
        p_.sleep(2);
        continue;
      }
      rxPos_ = 0;
      rxLen_ = r;
    }
    const char ch = (char)rx_[rxPos_++];
    if (ch == '\r') continue;
    if (ch != '\n') {
      if (n + 1 < sizeof(line)) line[n++] = ch;
      continue;
    }
    line[n] = 0;
    n = 0;
    if (first) {
      first = false;
      if (strlen(line) > 9) status = atoi(line + 9);
    } else if (!line[0]) {
      break;
    } else if (startsWithNoCase(line, "sec-websocket-accept:")) {
      const char *v = line + 21;
      while (*v == ' ') v++;
      accepted = strcmp(v, expected) == 0;
    }
  }
  if (status != 101 || !accepted) {
    error_ = status == 401 ? "This Wio isn't paired any more. Pair it again in Settings."
                           : "The server refused the terminal";
    closeCode_ = status == 401 ? 4001 : 0;
    tls_.close();
    return false;
  }
  open_ = true;
  return true;
}

bool WebSocket::sendFrame(uint8_t opcode, const uint8_t *data, size_t len) {
  if (!open_) return false;
  uint8_t frame[8 + 256];
  do {
    const size_t part = len > 256 ? 256 : len;
    size_t h = 0;
    frame[h++] = 0x80 | opcode;
    if (part < 126) {
      frame[h++] = 0x80 | (uint8_t)part;
    } else {
      frame[h++] = 0x80 | 126;
      frame[h++] = part >> 8;
      frame[h++] = part & 0xFF;
    }
    uint8_t mask[4];
    p_.random(mask, sizeof(mask));
    memcpy(frame + h, mask, 4);
    h += 4;
    for (size_t i = 0; i < part; i++) frame[h + i] = data[i] ^ mask[i % 4];
    if (!tls_.write(frame, h + part)) {
      open_ = false;
      return false;
    }
    data += part;
    len -= part;
  } while (len > 0);
  return true;
}

bool WebSocket::sendBinary(const uint8_t *data, size_t len) { return sendFrame(0x2, data, len); }

// One frame however long, unlike sendFrame, because a JSON message can't be split across frames.
bool WebSocket::sendText(const char *json) {
  if (!open_) return false;
  const size_t len = strlen(json);
  uint8_t head[8];
  size_t h = 0;
  head[h++] = 0x81;
  if (len < 126) {
    head[h++] = 0x80 | (uint8_t)len;
  } else {
    head[h++] = 0x80 | 126;
    head[h++] = len >> 8;
    head[h++] = len & 0xFF;
  }
  uint8_t mask[4];
  p_.random(mask, sizeof(mask));
  memcpy(head + h, mask, 4);
  h += 4;
  uint8_t out[256];
  memcpy(out, head, h);
  size_t used = h;
  for (size_t i = 0; i < len; i++) {
    out[used++] = (uint8_t)json[i] ^ mask[i % 4];
    if (used == sizeof(out) || i + 1 == len) {
      if (!tls_.write(out, used)) {
        open_ = false;
        return false;
      }
      used = 0;
    }
  }
  if (len == 0 && !tls_.write(out, used)) {
    open_ = false;
    return false;
  }
  return true;
}

bool WebSocket::sendAudio(const uint8_t *data, size_t len) {
  static char message[40 + MAX_AUDIO_CHUNK * 4 / 3 + 4];
  const int n = snprintf(message, sizeof(message), "{\"type\":\"dictate-audio\",\"data\":\"");
  base64(data, len, message + n);
  strcat(message, "\"}");
  return sendText(message);
}

void WebSocket::finishControl() {
  if (opcode_ == 0x9) {
    sendFrame(0xA, control_, controlLen_);
  } else if (opcode_ == 0x8) {
    if (controlLen_ >= 2) {
      closeCode_ = control_[0] << 8 | control_[1];
      const size_t n = controlLen_ - 2;
      memcpy(closeReason_, control_ + 2, n);
      closeReason_[n] = 0;
    } else {
      closeCode_ = 1005;
    }
    sendFrame(0x8, control_, controlLen_ >= 2 ? 2 : 0);
    open_ = false;
  }
}

int WebSocket::poll(uint8_t *out, size_t max) {
  size_t produced = 0;
  while (produced < max) {
    if (!open_ || textReady_) break;  // the caller takes a message before more is read
    if (rxPos_ == rxLen_) {
      const int r = tls_.read(rx_, sizeof(rx_));
      if (r < 0) {
        open_ = false;
        break;
      }
      if (r == 0) break;
      rxPos_ = 0;
      rxLen_ = r;
    }
    const uint8_t b = rx_[rxPos_];
    switch (state_) {
      case HEAD:
        rxPos_++;
        opcode_ = b & 0x0F;
        state_ = LEN;
        break;
      case LEN:
        rxPos_++;
        if (b & 0x80) {  // servers never mask
          open_ = false;
          break;
        }
        lenValue_ = b & 0x7F;
        if (lenValue_ >= 126) {
          lenBytes_ = lenValue_ == 126 ? 2 : 8;
          lenValue_ = 0;
          state_ = LEN_EXT;
          break;
        }
        left_ = lenValue_;
        controlLen_ = 0;
        textLen_ = 0;
        textTooLong_ = false;
        if (opcode_ & 0x8) {
          state_ = CONTROL;
          if (left_ == 0) {
            finishControl();
            state_ = HEAD;
          }
        } else {
          state_ = left_ ? PAYLOAD : HEAD;
        }
        break;
      case LEN_EXT:
        rxPos_++;
        lenValue_ = lenValue_ << 8 | b;
        if (--lenBytes_ == 0) {
          if (opcode_ & 0x8) {  // control frames are at most 125 bytes
            open_ = false;
            break;
          }
          left_ = lenValue_;
          textLen_ = 0;
          textTooLong_ = false;
          state_ = left_ ? PAYLOAD : HEAD;
        }
        break;
      case PAYLOAD: {
        size_t n = rxLen_ - rxPos_;
        if (n > left_) n = (size_t)left_;
        if (opcode_ == 0x1) {
          // The server's messages; terminal output is binary.
          if (textLen_ + n < sizeof(text_)) {
            memcpy(text_ + textLen_, rx_ + rxPos_, n);
            textLen_ += n;
          } else {
            textTooLong_ = true;
          }
          rxPos_ += n;
        } else {
          if (n > max - produced) n = max - produced;
          memcpy(out + produced, rx_ + rxPos_, n);
          produced += n;
          rxPos_ += n;
        }
        left_ -= n;
        if (left_ == 0) {
          state_ = HEAD;
          if (opcode_ == 0x1 && !textTooLong_) {
            text_[textLen_] = 0;
            textReady_ = true;
          }
        }
        break;
      }
      case CONTROL:
        rxPos_++;
        control_[controlLen_++] = b;
        if (--left_ == 0) {
          finishControl();
          state_ = HEAD;
        }
        break;
    }
  }
  if (produced > 0) return (int)produced;
  return open_ ? 0 : -1;
}

void WebSocket::close() {
  if (open_) {
    const uint8_t normal[2] = {0x03, 0xE8};
    sendFrame(0x8, normal, sizeof(normal));
  }
  open_ = false;
  tls_.close();
}
