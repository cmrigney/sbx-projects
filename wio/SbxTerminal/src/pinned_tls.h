// TLS to the sbx-projects server, trusting exactly one certificate by its SHA-256 fingerprint, as
// the iOS app does.
//
// This runs BearSSL on the SAMD51 rather than using the RTL8720's WiFiClientSecure, because that
// can't check a pin safely: its verify() always goes on to a hostname check, which this
// certificate can't pass (it names no addresses) and which overflows a heap buffer in the
// firmware.
#pragma once

#include <bearssl/bearssl.h>

#include "platform.h"

class PinnedTls {
 public:
  explicit PinnedTls(Platform &platform) : p_(platform) {}

  // With a pin, a different certificate fails the handshake. Without one, any certificate is
  // accepted and its fingerprint recorded: use that only to show it to the person pairing, and
  // send nothing over such a connection.
  bool connect(const char *host, uint16_t port, const uint8_t *pin);
  void close();

  // Returns the bytes read, 0 when nothing has arrived, or -1 once the connection has ended.
  int read(uint8_t *buf, size_t len);
  bool write(const uint8_t *buf, size_t len);

  const uint8_t *seenFingerprint() const { return x509_.seen; }
  bool pinMismatch() const { return x509_.mismatch; }
  const char *error() const { return error_; }

  // Implements br_x509_class; public for the C callbacks.
  struct PinX509 {
    const br_x509_class *vtable;
    br_sha256_context hash;
    br_x509_decoder_context decoder;
    const uint8_t *pin;
    uint8_t seen[32];
    uint8_t certIndex;
    bool mismatch;
  };

 private:
  bool flushRecords(uint32_t deadline);
  // Moves records between the socket and the engine. Returns false when the connection is gone.
  bool pump(bool &progress);

  Platform &p_;
  br_ssl_client_context client_;
  br_x509_minimal_context unusedX509_;
  PinX509 x509_;
  // Go's TLS sends records of up to 16 KB and has no way to ask for smaller ones.
  uint8_t iobuf_[BR_SSL_BUFSIZE_INPUT + 1024];
  bool open_ = false;
  const char *error_ = "";
};
