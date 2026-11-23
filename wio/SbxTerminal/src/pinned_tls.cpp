#include "pinned_tls.h"

#include <string.h>

static const uint32_t HANDSHAKE_MS = 20000;
static const uint32_t WRITE_MS = 10000;

// ---------------------------------------------------------------------------
// Certificate check: the first certificate must hash to the pin.
// ---------------------------------------------------------------------------

static void pinStartChain(const br_x509_class **ctx, const char *) {
  PinnedTls::PinX509 *x = (PinnedTls::PinX509 *)ctx;
  x->certIndex = 0;
  x->mismatch = false;
  memset(x->seen, 0, sizeof(x->seen));
}

static void pinStartCert(const br_x509_class **ctx, uint32_t) {
  PinnedTls::PinX509 *x = (PinnedTls::PinX509 *)ctx;
  if (x->certIndex == 0) {
    br_sha256_init(&x->hash);
    br_x509_decoder_init(&x->decoder, nullptr, nullptr);
  }
}

static void pinAppend(const br_x509_class **ctx, const unsigned char *buf, size_t len) {
  PinnedTls::PinX509 *x = (PinnedTls::PinX509 *)ctx;
  if (x->certIndex == 0) {
    br_sha256_update(&x->hash, buf, len);
    br_x509_decoder_push(&x->decoder, buf, len);
  }
}

static void pinEndCert(const br_x509_class **ctx) {
  PinnedTls::PinX509 *x = (PinnedTls::PinX509 *)ctx;
  if (x->certIndex == 0) br_sha256_out(&x->hash, x->seen);
  x->certIndex++;
}

static unsigned pinEndChain(const br_x509_class **ctx) {
  PinnedTls::PinX509 *x = (PinnedTls::PinX509 *)ctx;
  if (x->certIndex == 0 || br_x509_decoder_last_error(&x->decoder) != 0) return BR_ERR_X509_BAD_SERVER_NAME;
  if (br_x509_decoder_get_pkey(&x->decoder) == nullptr) return BR_ERR_X509_BAD_SERVER_NAME;
  if (x->pin) {
    uint8_t diff = 0;
    for (int i = 0; i < 32; i++) diff |= x->seen[i] ^ x->pin[i];
    if (diff != 0) {
      x->mismatch = true;
      return BR_ERR_X509_NOT_TRUSTED;
    }
  }
  return 0;
}

static const br_x509_pkey *pinGetPkey(const br_x509_class *const *ctx, unsigned *usages) {
  PinnedTls::PinX509 *x = (PinnedTls::PinX509 *)ctx;
  if (usages) *usages = BR_KEYTYPE_KEYX | BR_KEYTYPE_SIGN;
  return br_x509_decoder_get_pkey(&x->decoder);
}

static const br_x509_class kPinClass = {
    sizeof(PinnedTls::PinX509), pinStartChain, pinStartCert, pinAppend,
    pinEndCert,                 pinEndChain,   pinGetPkey,
};

// ---------------------------------------------------------------------------

bool PinnedTls::connect(const char *host, uint16_t port, const uint8_t *pin) {
  close();
  error_ = "";
  Transport &t = p_.transport();
  if (!t.open(host, port)) {
    error_ = "Can't reach the server";
    return false;
  }
  open_ = true;

  br_ssl_client_init_full(&client_, &unusedX509_, nullptr, 0);
  x509_.vtable = &kPinClass;
  x509_.pin = pin;
  x509_.mismatch = false;
  x509_.certIndex = 0;
  br_ssl_engine_set_x509(&client_.eng, &x509_.vtable);
  br_ssl_engine_set_versions(&client_.eng, BR_TLS12, BR_TLS12);
  br_ssl_engine_set_buffers_bidi(&client_.eng, iobuf_, BR_SSL_BUFSIZE_INPUT,
                                 iobuf_ + BR_SSL_BUFSIZE_INPUT, 1024);

  uint8_t seed[32];
  p_.random(seed, sizeof(seed));
  br_ssl_engine_inject_entropy(&client_.eng, seed, sizeof(seed));
  memset(seed, 0, sizeof(seed));

  if (!br_ssl_client_reset(&client_, nullptr, 0)) {
    error_ = "TLS setup failed";
    close();
    return false;
  }

  const uint32_t start = p_.ms();
  while (!(br_ssl_engine_current_state(&client_.eng) & BR_SSL_SENDAPP)) {
    bool progress = false;
    if (!pump(progress)) {
      error_ = x509_.mismatch ? "The server's certificate changed" : "Secure connection failed";
      close();
      return false;
    }
    if (p_.ms() - start > HANDSHAKE_MS) {
      error_ = "Secure connection timed out";
      close();
      return false;
    }
    if (!progress) p_.sleep(2);
  }
  return true;
}

void PinnedTls::close() {
  if (open_) p_.transport().close();
  open_ = false;
}

bool PinnedTls::pump(bool &progress) {
  const unsigned st = br_ssl_engine_current_state(&client_.eng);
  if (st & BR_SSL_CLOSED) return false;
  if (st & BR_SSL_SENDREC) {
    size_t len;
    uint8_t *buf = br_ssl_engine_sendrec_buf(&client_.eng, &len);
    const int n = p_.transport().send(buf, len);
    if (n < 0) return false;
    if (n > 0) {
      br_ssl_engine_sendrec_ack(&client_.eng, n);
      progress = true;
    }
    return true;
  }
  if (st & BR_SSL_RECVREC) {
    size_t len;
    uint8_t *buf = br_ssl_engine_recvrec_buf(&client_.eng, &len);
    const int n = p_.transport().recv(buf, len);
    if (n < 0) return false;
    if (n > 0) {
      br_ssl_engine_recvrec_ack(&client_.eng, n);
      progress = true;
    }
  }
  return true;
}

int PinnedTls::read(uint8_t *buf, size_t len) {
  if (!open_) return -1;
  for (;;) {
    const unsigned st = br_ssl_engine_current_state(&client_.eng);
    if (st & BR_SSL_RECVAPP) {
      size_t avail;
      const uint8_t *in = br_ssl_engine_recvapp_buf(&client_.eng, &avail);
      const size_t n = avail < len ? avail : len;
      memcpy(buf, in, n);
      br_ssl_engine_recvapp_ack(&client_.eng, n);
      return (int)n;
    }
    bool progress = false;
    if (!pump(progress)) return -1;
    if (!progress) return 0;
  }
}

bool PinnedTls::flushRecords(uint32_t deadline) {
  br_ssl_engine_flush(&client_.eng, 0);
  while (br_ssl_engine_current_state(&client_.eng) & BR_SSL_SENDREC) {
    bool progress = false;
    if (!pump(progress)) return false;
    if (p_.ms() - deadline < 0x80000000UL) return false;
    if (!progress) p_.sleep(1);
  }
  return true;
}

bool PinnedTls::write(const uint8_t *buf, size_t len) {
  if (!open_) return false;
  const uint32_t deadline = p_.ms() + WRITE_MS;
  while (len > 0) {
    const unsigned st = br_ssl_engine_current_state(&client_.eng);
    if (st & BR_SSL_CLOSED) return false;
    if (st & BR_SSL_SENDAPP) {
      size_t room;
      uint8_t *out = br_ssl_engine_sendapp_buf(&client_.eng, &room);
      const size_t n = room < len ? room : len;
      memcpy(out, buf, n);
      br_ssl_engine_sendapp_ack(&client_.eng, n);
      buf += n;
      len -= n;
      continue;
    }
    bool progress = false;
    if (!pump(progress)) return false;
    if (p_.ms() - deadline < 0x80000000UL) return false;
    if (!progress) p_.sleep(1);
  }
  return flushRecords(deadline);
}
