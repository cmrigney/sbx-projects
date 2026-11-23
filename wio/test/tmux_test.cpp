// Runs a real tmux at the Wio's size through the emulator and scrolls it the way the 5-way switch
// does. usage: tmux_test OUT.ppm
#include <fcntl.h>
#include <poll.h>
#include <pty.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>

#include <string>

#include "../SbxTerminal/src/keymap.h"
#include "../SbxTerminal/src/render.h"
#include "../SbxTerminal/src/vt.h"

static Grid a, b;
static int master;
static int failures = 0;

static void reply(void *, const uint8_t *d, size_t n) { (void)!write(master, d, n); }

static void pump(Vt &vt, int ms) {
  uint8_t buf[4096];
  pollfd pf{master, POLLIN, 0};
  while (poll(&pf, 1, ms) > 0) {
    const ssize_t n = read(master, buf, sizeof(buf));
    if (n <= 0) break;
    vt.feed(buf, n);
  }
}

static std::string text(const Vt &vt) {
  std::string out;
  for (int r = 1; r < GRID_ROWS; r++) {
    for (int c = 0; c < GRID_COLS; c++) {
      const uint16_t ch = vt.grid()->cells[r][c].ch;
      out += ch < 0x7F ? (char)ch : '?';
    }
    out += '\n';
  }
  return out;
}

static void check(bool ok, const char *what, const Vt &vt) {
  printf("%s  %s\n", ok ? "ok  " : "FAIL", what);
  if (!ok) {
    failures++;
    printf("%s", text(vt).c_str());
  }
}

static void wheel(const Vt &vt, bool up, int n) {
  char seq[24];
  const int len = snprintf(seq, sizeof(seq), "\x1b[<%d;%d;%dM", up ? 64 : 65, GRID_COLS / 2, TERM_ROWS / 2);
  for (int i = 0; i < n; i++) (void)!write(master, seq, len);
  (void)vt;
}

int main(int argc, char **argv) {
  Vt vt(&a, &b);
  vt.setReply(reply, nullptr);
  winsize ws{(unsigned short)TERM_ROWS, (unsigned short)GRID_COLS, 0, 0};
  char sock[64];
  snprintf(sock, sizeof(sock), "wio-test-%d", getpid());
  const pid_t pid = forkpty(&master, nullptr, nullptr, &ws);
  if (pid == 0) {
    setenv("TERM", "xterm-256color", 1);
    setenv("COLORTERM", "truecolor", 1);
    setenv("LANG", "C.UTF-8", 1);
    execlp("tmux", "tmux", "-u", "-T", "RGB", "-L", sock, "-f", "/dev/null", "new-session",
           "sh -c 'tmux set -g mouse on; tmux set -g history-limit 5000; "
           "seq -f \"history line %g\" 1 400; "
           "printf \"\\033[38;2;255;120;0morange\\033[0m \\342\\227\\217 \\342\\225\\255\\342\\224\\200\\342\\225\\256 \\344\\275\\240\\345\\245\\275 done\\n\"; cat'",
           (char *)nullptr);
    _exit(127);
  }
  pump(vt, 900);
  check(text(vt).find("history line 400") != std::string::npos, "tmux draws its output", vt);
  check(text(vt).find("orange") != std::string::npos, "truecolor text renders", vt);
  check(vt.mouseOn() && vt.mouseSgr(), "tmux turns on SGR mouse reporting", vt);

  wheel(vt, true, 30);
  pump(vt, 400);
  check(text(vt).find("history line 400") == std::string::npos, "wheel up scrolls into history", vt);
  check(text(vt).find("history line 2") != std::string::npos && text(vt).find("/374]") != std::string::npos,
        "tmux shows earlier lines in copy mode", vt);

  wheel(vt, false, 31);
  pump(vt, 400);
  check(text(vt).find("history line 400") != std::string::npos, "wheel down returns to the live screen", vt);

  // Typing reaches the program once back at the bottom.
  (void)!write(master, "typed-here\r", 11);
  pump(vt, 300);
  check(text(vt).find("typed-here") != std::string::npos, "keys reach the session", vt);

  if (argc > 1) {
    static uint16_t strip[STRIP_W * STRIP_H];
    FILE *f = fopen(argv[1], "wb");
    fprintf(f, "P6\n320 240\n255\n");
    for (int row = 0; row < GRID_ROWS; row++) {
      renderRow(vt.grid()->cells[row], -1, strip);
      for (int y = 0; y < 8; y++) {
        for (int x = 0; x < 320; x++) {
          const uint16_t c = strip[y * 320 + x];
          const uint8_t rgb[3] = {(uint8_t)(c >> 8 & 0xF8), (uint8_t)(c >> 3 & 0xFC), (uint8_t)(c << 3)};
          fwrite(rgb, 1, 3, f);
        }
      }
    }
    fclose(f);
  }

  char kill[128];
  snprintf(kill, sizeof(kill), "tmux -L %s kill-server 2>/dev/null", sock);
  (void)!system(kill);
  ::kill(pid, SIGTERM);
  waitpid(pid, nullptr, 0);
  printf("%s\n", failures ? "FAILED" : "PASSED");
  return failures ? 1 : 0;
}
