// The Wio's interface: browsing, the New sandbox form, pairing and the terminal.
#pragma once

#include "api.h"
#include "render.h"
#include "vt.h"

class App {
 public:
  explicit App(Platform &platform);

  void begin();
  void tick();

  // For tests.
  const Grid &screen() const { return *activeGrid(); }

 private:
  enum View : uint8_t {
    V_WIFI,
    V_WIFI_PASS,
    V_SERVER,
    V_CONFIRM,
    V_PROJECTS,
    V_SANDBOXES,
    V_DETAIL,
    V_NEW,
    V_TERMINAL,
    V_CLOSED,
    V_SETTINGS,
    V_NOTICE,
  };

  enum FieldType : uint8_t { F_TEXT, F_TOGGLE, F_CHOICE, F_BUTTON, F_NOTE };
  enum FieldId : uint8_t {
    FIELD_PASS, FIELD_HOST, FIELD_PORT, FIELD_CODE, FIELD_CONNECT,
    FIELD_PROJECT, FIELD_TASK, FIELD_SOURCE, FIELD_VALUE, FIELD_CLONE, FIELD_AGENT, FIELD_MODEL, FIELD_KIT, FIELD_ADD_KIT,
    FIELD_MCP, FIELD_CREATE, FIELD_NOTE,
  };
  struct Field {
    FieldType type;
    FieldId id;
    uint8_t index;
    const char *label;
    char *text;
    uint16_t cap;
    bool on;
    const char *value;
    bool disabled;
  };
  static const int MAX_FIELDS = 12 + MAX_KITS + MAX_MCPS;

  Grid *activeGrid() const;
  void go(View v);
  void draw();
  void flush();
  void clearUi();
  void header(const char *title);
  void footer(const char *hints);
  int text(int row, int col, const char *s, uint16_t fg, uint16_t bg, int maxCols);
  void busy(const char *message);
  void notice(const char *message, View back);
  int paragraph(int row, const char *s, uint16_t color);
  void applyFont();
  void changeFont(int delta);

  void handle(const Input &in);
  void handleList(const Input &in, int count);
  void handleForm(const Input &in);
  void handleTerminal(const Input &in);
  void activate();
  void goBack();

  void startNetwork();
  void scanWifi();
  void joinWifi();
  void startServerForm();
  void probeServer();
  void finishPairing();
  void loadHome();
  void loadSandboxes();
  void openNewForm(int projectIndex);
  void loadOptions();
  void buildNewForm();
  void buildServerForm();
  void buildPassForm();
  void changeChoice(Field &f, int delta);
  void toggleField(Field &f);
  void addKit();
  void submitNew();
  void sandboxAction(const char *verb);

  void attach(const char *name, bool shell);
  void pumpTerminal();
  void detach();
  void terminalClosed();
  void wheel(bool up, int notches);
  void drawTerminalBar();
  void startDictation();
  void finishDictation();
  void cancelDictation();
  void endMic();
  void dictationStatus(const char *message);
  void dictationLost();
  void pumpFormDictation();
  bool handleFormVoice(const Input &in);
  void insertTranscript(const char *spoken);
  void drawFormFooter();
  void pumpDictation();
  void handleServerMessage(char *json);
  bool typeTranscript(size_t maxBytes);
  static void vtReply(void *ctx, const uint8_t *data, size_t len);

  void drawProjects();
  void drawSandboxes();
  void drawDetail();
  void drawForm(const char *title);
  void drawWifi();
  void drawConfirm();
  void drawClosed();
  void drawSettings();
  void drawNotice();

  int visibleSandbox(int index) const;
  int visibleSandboxCount() const;
  SandboxInfo *selectedSandbox();
  ProjectInfo *projectById(int64_t id);

  Platform &p_;
  Config config_;
  PinnedTls tls_;
  Api api_;
  WebSocket ws_;
  Grid gridA_, gridB_;
  Vt vt_;
  uint16_t strip_[STRIP_W * STRIP_MAX_H];

  View view_ = V_NOTICE, noticeBack_ = V_PROJECTS;
  bool needsDraw_ = true;
  char noticeText_[160] = "";
  int sel_ = 0, top_ = 0;

  WifiNetwork networks_[16];
  int networkCount_ = 0;
  char portText_[6] = "7443";
  char codeText_[16] = "";
  char hostText_[64] = "";
  char passText_[64] = "";
  uint8_t pendingFp_[32];

  ProjectInfo projects_[MAX_PROJECTS];
  int projectCount_ = 0;
  SandboxInfo sandboxes_[MAX_SANDBOXES];
  int sandboxCount_ = 0;
  int64_t filterProject_ = -1;  // -1 for all projects
  char detailName_[48] = "";

  Field fields_[MAX_FIELDS];
  int fieldCount_ = 0, focus_ = 0, textCursor_ = 0, formTop_ = 0;
  SandboxOptions options_;
  bool optionsLoaded_ = false;
  char formError_[120] = "";
  int newProject_ = 0;
  char task_[512] = "";
  uint8_t sourceIndex_ = 0;
  char value_[120] = "";
  bool clone_ = true;
  uint8_t agentIndex_ = 0;
  uint8_t modelIndex_[MAX_AGENTS];
  char newKit_[64] = "";

  char termName_[48] = "";
  bool termShell_ = false, termOpen_ = false;
  bool canReattach_ = true;
  char closedText_[128] = "";
  int scrollDepth_ = 0;
  uint32_t lastFlush_ = 0, lastBlink_ = 0;
  bool blinkOn_ = true;
  int drawnCursorRow_ = -1, drawnCursorCol_ = -1;
  uint8_t termBuf_[1024];

  enum Dictation : uint8_t { DICT_IDLE, DICT_RECORDING, DICT_WAITING };
  MicStream mic_;
  Dictation dictation_ = DICT_IDLE;
  uint32_t dictationAt_ = 0, statusUntil_ = 0;
  char dictationStatus_[48] = "";
  uint8_t audioChunk_[WebSocket::MAX_AUDIO_CHUNK];
  char transcript_[1002];  // what's still to be typed
  size_t transcriptPos_ = 0, transcriptLen_ = 0;
  uint32_t lastTyped_ = 0, lastFooter_ = 0;
  bool aPending_ = false;  // A was tapped once and may be tapped again
  uint32_t aAt_ = 0;
  bool bPending_ = false;  // B was tapped once in an agent's terminal
  uint32_t bAt_ = 0;
};
