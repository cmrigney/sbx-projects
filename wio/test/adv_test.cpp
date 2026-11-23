// Advertisement parsing, including packets that lie about their lengths.
#include <stdio.h>

#include "../SbxTerminal/src/ble_adv.h"

static int failures = 0;
static void check(bool ok, const char *what) {
  printf("%s  %s\n", ok ? "ok  " : "FAIL", what);
  if (!ok) failures++;
}

int main() {
  AdvInfo a;
  const uint8_t named[] = {2, 0x01, 0x06, 17, 0x09, 'x', '1', ' ', 'p', 'l', 'u', 's', ' ',
                           'k', 'e', 'y', 'b', 'o', 'a', 'r', 'd'};
  parseAdv(named, sizeof(named), a);
  check(!strcmp(a.name, "x1 plus keyboard") && advIsKeyboard(a), "matches a keyboard by name");

  const uint8_t upper[] = {9, 0x09, 'K', 'E', 'Y', 'B', 'O', 'A', 'R', 'D'};
  parseAdv(upper, sizeof(upper), a);
  check(advIsKeyboard(a), "ignores the name's case");

  const uint8_t hid[] = {5, 0x03, 0x0F, 0x18, 0x12, 0x18};
  parseAdv(hid, sizeof(hid), a);
  check(a.hid && advIsKeyboard(a), "matches the HID service");

  const uint8_t appearance[] = {3, 0x19, 0xC1, 0x03};
  parseAdv(appearance, sizeof(appearance), a);
  check(advIsKeyboard(a), "matches the keyboard appearance");

  const uint8_t other[] = {14, 0x09, 'b', 'e', 'd', 'r', 'o', 'o', 'm', ' ', 'd', 'i', 's', 'p'};
  parseAdv(other, sizeof(other), a);
  check(!advIsKeyboard(a), "ignores other devices");

  const uint8_t overrun[] = {200, 0x09, 'k', 'e', 'y'};
  parseAdv(overrun, sizeof(overrun), a);
  check(!a.name[0], "stops at a field longer than the packet");

  const uint8_t zero[] = {0, 0x09, 'k'};
  parseAdv(zero, sizeof(zero), a);
  check(!a.name[0], "stops at an empty field");

  const uint8_t oddUuid[] = {2, 0x03, 0x12};
  parseAdv(oddUuid, sizeof(oddUuid), a);
  check(!a.hid, "ignores half a UUID");

  printf("%s\n", failures ? "FAILED" : "PASSED");
  return failures ? 1 : 0;
}
