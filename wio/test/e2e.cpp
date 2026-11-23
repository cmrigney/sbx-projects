// Drives the real firmware UI, TLS, HTTP and WebSocket code against `sbx-projects api --demo`.
// usage: e2e HOST PORT PAIRING_CODE FINGERPRINT_HEX OUT_DIR
#include <stdlib.h>

#include <string>

#include "../SbxTerminal/src/app.h"
#include "host_platform.h"

static int failures = 0;
#define CHECK(cond, what)                              \
  do {                                                 \
    printf("%s  %s\n", (cond) ? "ok  " : "FAIL", what); \
    if (!(cond)) failures++;                           \
  } while (0)

static std::string screenText(const Grid &g) {
  std::string out;
  for (int r = 0; r < GRID_ROWS; r++) {
    for (int c = 0; c < GRID_COLS; c++) {
      const uint16_t ch = g.cells[r][c].ch;
      out += ch < 0x7F ? (char)ch : '?';
    }
    out += '\n';
  }
  return out;
}

struct Rig {
  HostPlatform p;
  App *app;
  std::string dir;
  Rig(const std::string &d) : dir(d) { app = new App(p); }

  void run(int ms = 60) {
    const uint32_t start = p.ms();
    do {
      app->tick();
      p.sleep(2);
    } while (!p.keys.empty() || p.ms() - start < (uint32_t)ms);
  }
  bool waitFor(const char *text, int ms = 8000) {
    const uint32_t start = p.ms();
    while (p.ms() - start < (uint32_t)ms) {
      app->tick();
      if (screenText(app->screen()).find(text) != std::string::npos) return true;
      p.sleep(2);
    }
    printf("--- screen, waiting for \"%s\"\n%s", text, screenText(app->screen()).c_str());
    return false;
  }
  bool shows(const char *text) { return screenText(app->screen()).find(text) != std::string::npos; }
  void shot(const char *name) {
    run(90);
    p.screenshot(dir + "/" + name + ".ppm");
  }
};

int main(int argc, char **argv) {
  if (argc != 6) return 2;
  const char *host = argv[1];
  const uint16_t port = atoi(argv[2]);
  const char *code = argv[3];
  const std::string fp = argv[4];
  const std::string dir = argv[5];

  Rig r(dir);
  r.app->begin();
  CHECK(r.waitFor("Test Network"), "shows the Wi-Fi list");
  r.shot("01-wifi");
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("Password"), "asks for the password");
  r.p.type("hunter22");
  r.run();
  CHECK(r.shows("********") && !r.shows("hunter22"), "hides the password");
  r.p.press(KEY_DOWN);
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("Pair with your Mac"), "moves on to pairing");
  r.p.type(host);
  r.p.press(KEY_DOWN);
  for (int i = 0; i < 4; i++) r.p.press(KEY_BACKSPACE);
  r.p.type(argv[2]);
  r.p.press(KEY_DOWN);
  r.p.type(code);
  r.run();
  r.shot("02-pair");
  r.p.press(KEY_DOWN);
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("Check the certificate"), "shows the certificate to compare");
  char first[3] = {(char)toupper(fp[0]), (char)toupper(fp[1]), 0};
  CHECK(r.shows(first), "shows the server's real fingerprint");
  r.shot("03-certificate");
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("All sandboxes", 15000), "pairs and loads projects");
  r.shot("04-projects");

  CHECK(r.p.hasSaved && r.p.saved.paired && strlen(r.p.saved.token) == 64, "saves a token");
  char hex[65] = "";
  for (int i = 0; i < 32; i++) snprintf(hex + i * 2, 3, "%02x", r.p.saved.fingerprint[i]);
  CHECK(fp == hex, "pins the server's certificate");

  // A different pin must be refused, before anything is sent.
  {
    HostPlatform other;
    static PinnedTls tls(other);
    uint8_t wrong[32];
    memcpy(wrong, r.p.saved.fingerprint, 32);
    wrong[31] ^= 1;
    CHECK(!tls.connect(host, port, wrong) && tls.pinMismatch(), "refuses a certificate that doesn't match the pin");
    CHECK(tls.connect(host, port, r.p.saved.fingerprint), "accepts the pinned certificate");
    tls.close();
  }

  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("Enter open"), "opens all sandboxes");
  r.shot("05-sandboxes");
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("Attach to agent"), "opens a sandbox");
  r.shot("06-detail");

  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("demo: sbx exec", 15000), "attaches and shows terminal output");
  r.shot("07-terminal");
  r.p.press(KEY_ENTER);  // the demo session ends on Enter
  CHECK(r.waitFor("Detached", 8000), "reports the session ending");
  r.p.press(KEY_ESC);
  CHECK(r.waitFor("Attach to agent"), "returns to the sandbox");

  r.p.press(KEY_ESC);
  CHECK(r.waitFor("Enter open"), "goes back to the list");
  r.p.press(BTN_B);
  CHECK(r.waitFor("New sandbox", 25000), "opens the New sandbox form");
  CHECK(r.shows("Task") && r.shows("Agent") && r.shows("Private clone"), "offers the mobile form's fields");
  r.p.type("Add a health check");
  r.run();
  r.shot("08-new-sandbox");
  for (int i = 0; i < 32; i++) r.p.press(KEY_DOWN);
  r.run();
  CHECK(r.shows("Create sandbox"), "reaches the Create button");
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("Attach to agent", 15000), "creates the sandbox and opens it");
  CHECK(r.shows("Add a health check"), "the new sandbox has the task as its title");
  r.shot("09-created");

  printf("%s\n", failures ? "FAILED" : "PASSED");
  return failures ? 1 : 0;
}
