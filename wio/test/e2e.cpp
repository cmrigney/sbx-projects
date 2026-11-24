// Drives the real firmware UI, TLS, HTTP and WebSocket code against `sbx-projects api --demo`.
// usage: e2e HOST PORT PAIRING_CODE FINGERPRINT_HEX OUT_DIR
#include <math.h>
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
  for (int r = 0; r < gridRows; r++) {
    for (int c = 0; c < gridCols; c++) {
      const uint16_t ch = g.cells[r][c].ch;
      out += ch < 0x7F ? (char)ch : '?';
    }
    out += '\n';
  }
  return out;
}

// A WAV of 16-bit 16 kHz mono, as the ADC's 12-bit readings around mid-scale at a quiet level; or a
// second of tone when there's no file.
static std::vector<uint16_t> micSamples() {
  std::vector<uint16_t> out;
  const char *path = getenv("MIC_WAV");
  if (path && *path) {
    FILE *f = fopen(path, "rb");
    if (!f) abort();
    fseek(f, 44, SEEK_SET);
    int16_t s;
    while (fread(&s, 2, 1, f) == 1) out.push_back((uint16_t)(1900 + s / 64));
    fclose(f);
  } else {
    for (int i = 0; i < 16000; i++) out.push_back((uint16_t)(1900 + 300 * sin(i * 0.2)));
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
  CHECK(r.waitFor("Enter  C back"), "opens all sandboxes");
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
  CHECK(r.waitFor("Enter  C back"), "goes back to the list");
  r.p.press(BTN_B);
  CHECK(r.waitFor("New sandbox", 25000), "opens the New sandbox form");
  CHECK(r.shows("Task") && r.shows("Agent") && r.shows("Clone"), "offers the mobile form's fields");
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

  // Text size, from Settings, saved, and used by the terminal.
  r.p.press(KEY_ESC);
  CHECK(r.waitFor("Enter  C back"), "back at the sandbox list");
  r.p.press(KEY_ESC);
  CHECK(r.waitFor("All sandboxes"), "back at the projects");
  for (int i = 0; i < 40; i++) r.p.press(KEY_DOWN);
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("Text size"), "opens Settings");
  CHECK(r.shows("Small 45x17"), "starts at the small size");
  r.p.press(KEY_RIGHT);
  CHECK(r.waitFor("Medium 35x15") && r.p.saved.fontSize == 1, "changes to medium and saves it");
  r.shot("12-settings-medium");
  r.p.press(KEY_RIGHT);
  CHECK(r.waitFor("Large 32x11") && r.p.saved.fontSize == 2, "changes to large and saves it");
  r.shot("13-settings-large");
  r.p.press(KEY_ESC);
  CHECK(r.waitFor("All sandboxes"), "projects fit at the large size");
  r.shot("14-projects-large");
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("Enter  C back"), "sandboxes fit at the large size");
  r.shot("15-sandboxes-large");
  r.p.press(KEY_DOWN);  // past the sandbox made above, which is still being created
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("Attach to agent") && r.shows("Stop sandbox"), "every action fits at the large size");
  r.shot("16-detail-large");
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("demo: sbx exec", 15000), "attaches at the large size");
  r.shot("17-terminal-large");
  r.p.press(BTN_C);
  CHECK(r.waitFor("Attach to agent"), "detaches at the large size");

  // Dictation: the mic's audio goes to the Mac and its text is typed, without pressing Enter.
  r.p.micSamples = micSamples();
  r.p.press(KEY_UP);
  r.run();
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("demo: sbx exec", 15000), "attaches for dictation");
  r.run(300);
  r.p.press(BTN_A);
  CHECK(r.waitFor("REC"), "shows that it's recording");
  r.shot("18-recording");
  r.run((int)(r.p.micSamples.size() / 16) + 300);
  r.p.press(BTN_A);
  CHECK(r.waitFor("Transcribing"), "sends the recording");
  r.shot("19-transcribing");
  const std::string expected = getenv("MIC_EXPECT");
  CHECK(r.waitFor(expected.substr(0, 12).c_str(), 45000), "types the transcript");
  r.run(600);
  r.shot("20-transcript");
  CHECK(!r.shows("Detached"), "doesn't press Enter");
  const auto inked = [&] {
    int n = 0;
    for (char c : screenText(r.app->screen())) n += c != ' ' && c != '\n';
    return n;
  };
  const int before = inked();
  r.p.press(KEY_BACKSPACE);  // the space after the transcript
  r.p.press(KEY_BACKSPACE);  // its last character
  r.run(300);
  CHECK(inked() == before - 1, "the transcript can be backspaced");
  r.p.press(BTN_A);
  CHECK(r.waitFor("REC"), "records again");
  r.p.press(BTN_B);
  CHECK(r.waitFor("Dictation cancelled"), "cancels a recording");
  r.p.press(BTN_C);
  CHECK(r.waitFor("Attach to agent"), "detaches after dictating");

  // Without a keyboard: double-tapping A is Enter, and holding B is Ctrl+C.
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("demo: sbx exec", 15000), "attaches again");
  r.run(300);
  r.p.press(BTN_A);
  r.p.press(BTN_A);
  CHECK(r.waitFor("Detached", 8000), "double-tapping A presses Enter");
  r.p.press(KEY_ESC);
  CHECK(r.waitFor("Attach to agent"), "back at the sandbox");
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("demo: sbx exec", 15000), "attaches once more");
  r.run(300);
  r.p.press(BTN_B_HOLD);
  CHECK(r.waitFor("session ended", 8000), "holding B sends Ctrl+C");
  r.p.press(KEY_ESC);
  CHECK(r.waitFor("Attach to agent"), "back at the sandbox after Ctrl+C");

  // Double-tapping B is Esc in an agent's terminal, which the demo's tty echoes as ^[.
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("demo: sbx exec", 15000), "attaches for Esc");
  r.run(300);
  r.p.press(BTN_B);
  r.run(500);
  CHECK(!r.shows("^["), "a single tap of B sends nothing");
  r.p.press(BTN_B);
  r.run(500);
  CHECK(!r.shows("^["), "two slow taps of B send nothing");
  r.p.press(BTN_B);
  r.p.press(BTN_B);
  CHECK(r.waitFor("^["), "double-tapping B presses Esc");
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("Detached", 8000), "the session is still live after Esc");
  r.p.press(KEY_ESC);
  CHECK(r.waitFor("Attach to agent"), "back at the sandbox after Esc");
  r.p.press(KEY_DOWN);
  r.run();
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("demo: sbx exec", 15000), "opens a shell");
  r.run(300);
  r.p.press(BTN_B);
  r.p.press(BTN_B);
  r.run(400);
  CHECK(!r.shows("^["), "double-tapping B does nothing in a shell");
  r.p.press(KEY_ENTER);
  CHECK(r.waitFor("shell exited", 8000), "the shell ends");
  r.p.press(KEY_ESC);
  CHECK(r.waitFor("Attach to agent"), "back at the sandbox after the shell");
  r.p.press(KEY_UP);
  r.run();

  // The same controls in the New sandbox form.
  const std::string end = getenv("MIC_EXPECT_END");
  const auto dictate = [&] {
    r.p.press(BTN_A);
    CHECK(r.waitFor("REC", 8000), "records into a field");
    r.run((int)(r.p.micSamples.size() / 16) + 200);
    r.p.press(BTN_A);
    CHECK(r.waitFor("Transcribing"), "transcribes for a field");
  };
  r.p.press(KEY_ESC);
  CHECK(r.waitFor("Enter  C back"), "back at the list for the form");
  r.p.press(BTN_B);
  CHECK(r.waitFor("New sandbox", 25000), "opens the form");
  CHECK(r.waitFor("hold B clear"), "shows the voice controls on a text field");
  dictate();
  CHECK(r.waitFor(end.c_str(), 45000), "puts the words in the Task field");
  r.shot("21-form-dictated");
  r.p.press(BTN_B_HOLD);
  r.run(150);
  CHECK(!r.shows(end.c_str()), "holding B clears the field");
  r.p.press(BTN_A);
  r.p.press(BTN_A);
  CHECK(r.waitFor("L/R change"), "double-tapping A moves to the next field");
  r.p.press(KEY_DOWN);
  r.run();
  dictate();
  CHECK(r.waitFor(("-" + end).c_str(), 45000), "makes a branch name from the words");
  r.shot("22-form-branch");
  r.p.press(BTN_C);
  CHECK(r.waitFor("All sandboxes"), "leaves the form");  // opened from all sandboxes

  printf("%s\n", failures ? "FAILED" : "PASSED");
  return failures ? 1 : 0;
}
