// The microphone's audio on its way to the Mac: 12-bit readings, centered and packed as IMA ADPCM
// at 8 KB/s, in a ring that an interrupt fills and the main loop drains. The buffer is allocated
// only while recording, as the Bluetooth and Wi-Fi libraries need the heap.
#pragma once

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

class MicStream {
 public:
  static const size_t CAPACITY = 8192;  // one second, to ride out a slow moment on the network

  bool begin() {
    end();
    buf_ = (uint8_t *)malloc(CAPACITY);
    if (!buf_) return false;
    head_ = tail_ = 0;
    overflowed_ = false;
    started_ = half_ = false;
    predicted_ = 0;
    index_ = 0;
    peak_ = 0;
    return true;
  }

  void end() {
    uint8_t *b = buf_;
    buf_ = nullptr;
    free(b);
  }

  // From the interrupt. After an overflow nothing more is stored, because ADPCM can't skip.
  inline void push(uint16_t raw) {
    if (!buf_ || overflowed_) return;
    const int32_t scaled = (int32_t)raw << 16;
    if (!started_) {
      dc_ = scaled;
      started_ = true;
    }
    dc_ += (scaled - dc_) >> 11;  // follows the mic's bias, not speech
    int32_t sample = (scaled - dc_) >> 12;
    sample = sample > 32767 ? 32767 : sample < -32768 ? -32768 : sample;
    const uint16_t magnitude = (uint16_t)(sample < 0 ? -sample : sample);
    if (magnitude > peak_) peak_ = magnitude;

    const uint8_t code = encode((int16_t)sample);
    if (!half_) {
      nibble_ = code;
      half_ = true;
      return;
    }
    half_ = false;
    const size_t next = (head_ + 1) % CAPACITY;
    if (next == tail_) {
      overflowed_ = true;
      return;
    }
    buf_[head_] = nibble_ | code << 4;
    head_ = next;
  }

  size_t available() const {
    const size_t h = head_, t = tail_;
    return h >= t ? h - t : CAPACITY - t + h;
  }

  size_t read(uint8_t *out, size_t max) {
    size_t n = 0;
    size_t t = tail_;
    const size_t h = head_;
    while (n < max && t != h) {
      out[n++] = buf_[t];
      t = (t + 1) % CAPACITY;
    }
    tail_ = t;
    return n;
  }

  bool overflowed() const { return overflowed_; }

  // 0 to 8, for the meter. Resets the peak.
  uint8_t takeLevel() {
    uint16_t p = peak_;
    peak_ = 0;
    uint8_t level = 0;
    for (p >>= 5; p && level < 8; p >>= 1) level++;
    return level;
  }

 private:
  inline uint8_t encode(int16_t sample) {
    static const int16_t steps[89] = {
        7, 8, 9, 10, 11, 12, 13, 14, 16, 17, 19, 21, 23, 25, 28, 31, 34, 37, 41, 45, 50, 55, 60, 66, 73, 80, 88, 97,
        107, 118, 130, 143, 157, 173, 190, 209, 230, 253, 279, 307, 337, 371, 408, 449, 494, 544, 598, 658, 724, 796,
        876, 963, 1060, 1166, 1282, 1411, 1552, 1707, 1878, 2066, 2272, 2499, 2749, 3024, 3327, 3660, 4026, 4428, 4871,
        5358, 5894, 6484, 7132, 7845, 8630, 9493, 10442, 11487, 12635, 13899, 15289, 16818, 18500, 20350, 22385, 24623,
        27086, 29794, 32767};
    static const int8_t adjust[8] = {-1, -1, -1, -1, 2, 4, 6, 8};
    const int32_t step = steps[index_];
    int32_t delta = sample - predicted_;
    uint8_t code = 0;
    if (delta < 0) {
      code = 8;
      delta = -delta;
    }
    int32_t diff = step >> 3;
    if (delta >= step) {
      code |= 4;
      delta -= step;
      diff += step;
    }
    if (delta >= step >> 1) {
      code |= 2;
      delta -= step >> 1;
      diff += step >> 1;
    }
    if (delta >= step >> 2) {
      code |= 1;
      diff += step >> 2;
    }
    int32_t next = predicted_ + (code & 8 ? -diff : diff);
    predicted_ = next > 32767 ? 32767 : next < -32768 ? -32768 : next;
    int idx = index_ + adjust[code & 7];
    index_ = idx < 0 ? 0 : idx > 88 ? 88 : idx;
    return code;
  }

  uint8_t *volatile buf_ = nullptr;
  volatile size_t head_ = 0, tail_ = 0;
  volatile bool overflowed_ = false;
  volatile uint16_t peak_ = 0;
  bool started_ = false, half_ = false;
  uint8_t nibble_ = 0;
  int32_t dc_ = 0, predicted_ = 0;
  uint8_t index_ = 0;
};
