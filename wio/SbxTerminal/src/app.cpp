#include "app.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include <ArduinoJson.h>

#include "keymap.h"

// The apps' dark theme.
static const uint16_t C_BG = VT_DEFAULT_BG;
static const uint16_t C_PANEL = rgb565(0x26, 0x26, 0x26);
static const uint16_t C_SELECT = rgb565(0x4A, 0x2E, 0x24);
static const uint16_t C_FIELD = rgb565(0x2C, 0x2C, 0x2C);
static const uint16_t C_TEXT = VT_DEFAULT_FG;
static const uint16_t C_MUTED = rgb565(0xB0, 0xB0, 0xB0);
static const uint16_t C_ACCENT = VT_CARET;
static const uint16_t C_OK = rgb565(0x4E, 0xBA, 0x65);
static const uint16_t C_WARN = rgb565(0xFF, 0xC1, 0x07);
static const uint16_t C_FAIL = rgb565(0xFF, 0x6B, 0x80);

static const int LIST_TOP = 1;
#define LIST_BOTTOM (gridRows - 2)  // the last row holds the key hints
static const uint32_t FLUSH_MS = 40;
static const uint32_t BLINK_MS = 500;
static const int PAGE_NOTCHES = 5;
static const uint32_t MAX_RECORDING_MS = 45000;
static const uint32_t TRANSCRIBE_WAIT_MS = 70000;  // the server gives up after 60 s
static const uint32_t STATUS_MS = 6000;
static const uint32_t TYPE_EVERY_MS = 25;
static const size_t TYPE_BYTES = 12;  // about 480 characters a second
static const uint32_t DOUBLE_TAP_MS = 350;
static const uint32_t REQUEST_WAIT_MS = 8000;
static const int MENU_ITEMS = 3;
static const int MENU_ROWS = MENU_ITEMS + 1;  // with its title
static const char *const MENU_LABELS[MENU_ITEMS] = {"Review PR in new sandbox", "Copy last reply", "Paste copied reply"};

static uint16_t stateColor(const SandboxInfo &s) {
  if (!strcmp(s.state, "needsYou")) return C_WARN;
  if (!strcmp(s.state, "failed")) return C_FAIL;
  if (!strcmp(s.state, "working") || !strcmp(s.state, "starting")) return C_ACCENT;
  if (!strcmp(s.state, "ready")) return C_OK;
  return C_MUTED;
}

static bool isUp(Key k) { return k == SW_UP || k == KEY_UP; }
static bool isDown(Key k) { return k == SW_DOWN || k == KEY_DOWN; }
static bool isSelect(Key k) { return k == SW_PRESS || k == KEY_ENTER; }
static bool isBack(Key k) { return k == BTN_C || k == KEY_ESC; }

App::App(Platform &platform)
    : p_(platform), tls_(platform), api_(platform, config_, tls_), ws_(platform, tls_),
      vt_(&gridA_, &gridB_) {
  memset(&config_, 0, sizeof(config_));
  vt_.setReply(vtReply, this);
}

Grid *App::activeGrid() const { return view_ == V_TERMINAL ? vt_.grid() : const_cast<Grid *>(&gridA_); }

// ---------------------------------------------------------------------------
// Start-up
// ---------------------------------------------------------------------------

void App::begin() {
  if (!p_.loadConfig(config_)) {
    memset(&config_, 0, sizeof(config_));
    config_.port = 7443;
  }
  if (config_.port == 0) config_.port = 7443;
  if (config_.fontSize >= fontCount()) config_.fontSize = 0;
  applyFont();
  startNetwork();
}

void App::applyFont() {
  setFont(config_.fontSize);
  vt_.reset();
  p_.clearScreen(C_BG);
}

void App::changeFont(int delta) {
  config_.fontSize = (config_.fontSize + delta + fontCount()) % fontCount();
  applyFont();
  p_.saveConfig(config_);
  needsDraw_ = true;
}

void App::startNetwork() {
  if (!config_.ssid[0]) {
    scanWifi();
    return;
  }
  char line[80];
  snprintf(line, sizeof(line), "Joining %s", config_.ssid);
  busy(line);
  if (!p_.wifiJoin(config_.ssid, config_.pass)) {
    scanWifi();
    return;
  }
  if (config_.paired) loadHome();
  else startServerForm();
}

void App::scanWifi() {
  busy("Looking for Wi-Fi networks");
  networkCount_ = p_.wifiScan(networks_, 16);
  sel_ = top_ = 0;
  go(V_WIFI);
}

void App::joinWifi() {
  char line[80];
  snprintf(line, sizeof(line), "Joining %s", config_.ssid);
  busy(line);
  if (!p_.wifiJoin(config_.ssid, config_.pass)) {
    snprintf(formError_, sizeof(formError_), "Couldn't join %s. Check the password.", config_.ssid);
    buildPassForm();
    go(V_WIFI_PASS);
    return;
  }
  p_.saveConfig(config_);
  if (config_.paired) loadHome();
  else startServerForm();
}

void App::startServerForm() {
  snprintf(hostText_, sizeof(hostText_), "%s", config_.host);
  snprintf(portText_, sizeof(portText_), "%u", (unsigned)config_.port);
  codeText_[0] = 0;
  formError_[0] = 0;
  buildServerForm();
  go(V_SERVER);
}

void App::probeServer() {
  const long port = atol(portText_);
  if (!hostText_[0] || port < 1 || port > 65535) {
    snprintf(formError_, sizeof(formError_), "Enter the Mac's address and port.");
    buildServerForm();
    needsDraw_ = true;
    return;
  }
  if (strlen(codeText_) < 8) {
    snprintf(formError_, sizeof(formError_), "Enter the 8-character pairing code.");
    buildServerForm();
    needsDraw_ = true;
    return;
  }
  busy("Reading the server's certificate");
  if (!api_.probeCertificate(hostText_, (uint16_t)port, pendingFp_)) {
    snprintf(formError_, sizeof(formError_), "%s", api_.error());
    buildServerForm();
    go(V_SERVER);
    return;
  }
  go(V_CONFIRM);
}

void App::finishPairing() {
  // A failed attempt leaves the pairing that was working in place.
  const Config previous = config_;
  snprintf(config_.host, sizeof(config_.host), "%s", hostText_);
  config_.port = (uint16_t)atol(portText_);
  memcpy(config_.fingerprint, pendingFp_, 32);
  busy("Pairing");
  if (!api_.pair(codeText_)) {
    snprintf(formError_, sizeof(formError_), "%s", api_.error());
    config_ = previous;
    buildServerForm();
    go(V_SERVER);
    return;
  }
  memset(codeText_, 0, sizeof(codeText_));
  const bool saved = p_.saveConfig(config_);
  loadHome();
  if (!saved && view_ == V_PROJECTS) {
    notice("Paired, but the settings couldn't be saved. You'll need to set up again after a "
           "restart. The serial log has the reason.", V_PROJECTS);
  }
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

void App::loadHome() {
  busy("Loading projects");
  const int n = api_.projects(projects_, MAX_PROJECTS);
  if (n < 0) {
    if (api_.unauthorized()) {
      config_.paired = false;
      p_.saveConfig(config_);
      snprintf(formError_, sizeof(formError_), "This Wio isn't paired any more. Pair it again.");
      snprintf(hostText_, sizeof(hostText_), "%s", config_.host);
      buildServerForm();
      go(V_SERVER);
      return;
    }
    notice(api_.error(), V_SETTINGS);
    return;
  }
  projectCount_ = n;
  const int s = api_.sandboxes(sandboxes_, MAX_SANDBOXES);
  if (s < 0) {
    notice(api_.error(), V_SETTINGS);
    return;
  }
  sandboxCount_ = s;
  for (int i = 0; i < sandboxCount_; i++) {
    if (ProjectInfo *pr = projectById(sandboxes_[i].projectId)) pr->sandboxes++;
  }
  if (view_ != V_PROJECTS) {
    sel_ = top_ = 0;
  }
  go(V_PROJECTS);
}

void App::loadSandboxes() {
  busy("Loading sandboxes");
  const int s = api_.sandboxes(sandboxes_, MAX_SANDBOXES);
  if (s < 0) {
    notice(api_.error(), V_PROJECTS);
    return;
  }
  sandboxCount_ = s;
  for (int i = 0; i < projectCount_; i++) projects_[i].sandboxes = 0;
  for (int i = 0; i < sandboxCount_; i++) {
    if (ProjectInfo *pr = projectById(sandboxes_[i].projectId)) pr->sandboxes++;
  }
}

ProjectInfo *App::projectById(int64_t id) {
  for (int i = 0; i < projectCount_; i++) {
    if (projects_[i].id == id) return &projects_[i];
  }
  return nullptr;
}

int App::visibleSandboxCount() const {
  if (filterProject_ < 0) return sandboxCount_;
  int n = 0;
  for (int i = 0; i < sandboxCount_; i++) {
    if (sandboxes_[i].projectId == filterProject_) n++;
  }
  return n;
}

int App::visibleSandbox(int index) const {
  for (int i = 0; i < sandboxCount_; i++) {
    if (filterProject_ >= 0 && sandboxes_[i].projectId != filterProject_) continue;
    if (index-- == 0) return i;
  }
  return -1;
}

SandboxInfo *App::selectedSandbox() {
  for (int i = 0; i < sandboxCount_; i++) {
    if (!strcmp(sandboxes_[i].name, detailName_)) return &sandboxes_[i];
  }
  return nullptr;
}

// ---------------------------------------------------------------------------
// Forms
// ---------------------------------------------------------------------------

void App::buildPassForm() {
  fieldCount_ = 0;
  fields_[fieldCount_++] = Field{F_TEXT, FIELD_PASS, 0, "Password", passText_, sizeof(passText_), false, nullptr, false};
  fields_[fieldCount_++] = Field{F_BUTTON, FIELD_CONNECT, 0, "Join", nullptr, 0, false, nullptr, false};
  focus_ = 0;
  formTop_ = 0;
  textCursor_ = strlen(passText_);
}

void App::buildServerForm() {
  fieldCount_ = 0;
  fields_[fieldCount_++] = Field{F_TEXT, FIELD_HOST, 0, "Mac", hostText_, sizeof(hostText_), false, nullptr, false};
  fields_[fieldCount_++] = Field{F_TEXT, FIELD_PORT, 0, "Port", portText_, sizeof(portText_), false, nullptr, false};
  fields_[fieldCount_++] = Field{F_TEXT, FIELD_CODE, 0, "Code", codeText_, sizeof(codeText_), false, nullptr, false};
  fields_[fieldCount_++] = Field{F_BUTTON, FIELD_CONNECT, 0, "Continue", nullptr, 0, false, nullptr, false};
  focus_ = hostText_[0] ? 2 : 0;
  formTop_ = 0;
  textCursor_ = strlen(fields_[focus_].text);
}

void App::openNewForm(int projectIndex, const char *pullRequest) {
  newProject_ = projectIndex < 0 ? 0 : projectIndex;
  task_[0] = value_[0] = newKit_[0] = 0;
  sourceIndex_ = pullRequest ? 1 : 0;
  if (pullRequest) snprintf(value_, sizeof(value_), "%s", pullRequest);
  formError_[0] = 0;
  if (projectCount_ == 0) {
    notice("Add a project on the Mac first.", V_PROJECTS);
    return;
  }
  loadOptions();
}

void App::loadOptions() {
  busy("Loading the Mac's options");
  optionsLoaded_ = api_.options(projects_[newProject_].id, options_);
  if (!optionsLoaded_) {
    notice(api_.error(), V_PROJECTS);
    return;
  }
  agentIndex_ = options_.defaultAgent;
  clone_ = options_.defaultClone;
  for (int i = 0; i < MAX_AGENTS; i++) modelIndex_[i] = options_.agents[i].defaultModel;
  focus_ = 0;
  formTop_ = 0;
  buildNewForm();
  for (int i = 0; i < fieldCount_; i++) {
    if (fields_[i].id == FIELD_TASK) focus_ = i;
  }
  textCursor_ = strlen(task_);
  go(V_NEW);
}

void App::buildNewForm() {
  int n = 0;
  const auto add = [&](const Field &f) {
    if (n < MAX_FIELDS) fields_[n++] = f;
  };
  add(Field{F_CHOICE, FIELD_PROJECT, 0, "Project", nullptr, 0, false, projects_[newProject_].name, projectCount_ < 2});
  add(Field{F_TEXT, FIELD_TASK, 0, "Task", task_, sizeof(task_), false, nullptr, false});
  const bool pr = sourceIndex_ == 1;
  add(Field{F_CHOICE, FIELD_SOURCE, 0, "From", nullptr, 0, false, pr ? "Pull request" : "New branch", false});
  add(Field{F_TEXT, FIELD_VALUE, 0, pr ? "PR" : "Branch", value_, sizeof(value_), false, nullptr, false});
  if (!pr) add(Field{F_TOGGLE, FIELD_CLONE, 0, "Clone", nullptr, 0, clone_, "private", false});
  const AgentOption &agent = options_.agents[agentIndex_];
  add(Field{F_CHOICE, FIELD_AGENT, 0, "Agent", nullptr, 0, false, agent.label, options_.agentCount < 2});
  if (agent.modelCount > 0) {
    add(Field{F_CHOICE, FIELD_MODEL, 0, "Model", nullptr, 0, false, agent.models[modelIndex_[agentIndex_]].label, agent.modelCount < 2});
  }
  for (int i = 0; i < options_.kitCount; i++) {
    add(Field{F_TOGGLE, FIELD_KIT, (uint8_t)i, "Kit", nullptr, 0, options_.kitPicked[i], options_.kits[i], false});
  }
  if (options_.kitCount < MAX_KITS) {
    add(Field{F_TEXT, FIELD_ADD_KIT, 0, "Add kit", newKit_, sizeof(newKit_), false, nullptr, false});
  }
  for (int i = 0; i < options_.mcpCount; i++) {
    const McpOption &m = options_.mcps[i];
    add(Field{F_TOGGLE, FIELD_MCP, (uint8_t)i, m.ready ? "MCP" : "MCP*", nullptr, 0, m.picked, m.name, !m.ready && !m.picked});
  }
  if (options_.mcpError[0]) add(Field{F_NOTE, FIELD_NOTE, 0, options_.mcpError, nullptr, 0, false, nullptr, true});
  add(Field{F_BUTTON, FIELD_CREATE, 0, "Create sandbox", nullptr, 0, false, nullptr, false});
  fieldCount_ = n;
  if (focus_ >= fieldCount_) focus_ = fieldCount_ - 1;
}

void App::changeChoice(Field &f, int delta) {
  if (f.disabled) return;
  const auto step = [delta](int v, int count) { return (v + delta + count) % count; };
  switch (f.id) {
    case FIELD_PROJECT:
      newProject_ = step(newProject_, projectCount_);
      loadOptions();
      if (view_ == V_NEW) {
        focus_ = 0;
        needsDraw_ = true;
      }
      return;
    case FIELD_SOURCE:
      sourceIndex_ = !sourceIndex_;
      break;
    case FIELD_AGENT:
      agentIndex_ = step(agentIndex_, options_.agentCount);
      break;
    case FIELD_MODEL:
      modelIndex_[agentIndex_] = step(modelIndex_[agentIndex_], options_.agents[agentIndex_].modelCount);
      break;
    default:
      return;
  }
  buildNewForm();
  needsDraw_ = true;
}

void App::toggleField(Field &f) {
  if (f.disabled) return;
  if (f.id == FIELD_CLONE) clone_ = !clone_;
  else if (f.id == FIELD_KIT) options_.kitPicked[f.index] = !options_.kitPicked[f.index];
  else if (f.id == FIELD_MCP) options_.mcps[f.index].picked = !options_.mcps[f.index].picked;
  buildNewForm();
  needsDraw_ = true;
}

void App::addKit() {
  if (!newKit_[0] || options_.kitCount >= MAX_KITS) return;
  int at = -1;
  for (int i = 0; i < options_.kitCount; i++) {
    if (!strcmp(options_.kits[i], newKit_)) at = i;
  }
  if (at < 0) {
    at = options_.kitCount++;
    snprintf(options_.kits[at], sizeof(options_.kits[at]), "%s", newKit_);
  }
  options_.kitPicked[at] = true;
  newKit_[0] = 0;
  textCursor_ = 0;
  buildNewForm();
  for (int i = 0; i < fieldCount_; i++) {
    if (fields_[i].id == FIELD_ADD_KIT) focus_ = i;
  }
  needsDraw_ = true;
}

void App::submitNew() {
  const bool pr = sourceIndex_ == 1;
  if (pr && !value_[0]) {
    snprintf(formError_, sizeof(formError_), "Enter the pull request's number or URL.");
    needsDraw_ = true;
    return;
  }
  const AgentOption &agent = options_.agents[agentIndex_];
  NewSandbox r;
  memset(&r, 0, sizeof(r));
  r.projectId = projects_[newProject_].id;
  r.agent = agent.id;
  r.source = pr ? "pr" : "branch";
  r.value = value_;
  r.task = task_;
  r.clone = pr || clone_;
  r.model = agent.modelCount ? agent.models[modelIndex_[agentIndex_]].id : "";
  bool tooLong = false;
  for (int i = 0; i < options_.kitCount; i++) {
    if (!options_.kitPicked[i]) continue;
    tooLong = tooLong || options_.kitTooLong[i];
    r.kits[r.kitCount++] = options_.kits[i];
  }
  for (int i = 0; i < options_.mcpCount; i++) {
    if (!options_.mcps[i].picked) continue;
    tooLong = tooLong || options_.mcps[i].tooLong;
    r.mcps[r.mcpCount++] = options_.mcps[i].name;
  }
  if (tooLong) {
    snprintf(formError_, sizeof(formError_), "A kit or MCP name is too long. Turn it off.");
    needsDraw_ = true;
    return;
  }
  busy("Creating the sandbox");
  char created[48] = "";
  if (!api_.create(r, created, sizeof(created))) {
    snprintf(formError_, sizeof(formError_), "%s", api_.error());
    go(V_NEW);
    return;
  }
  filterProject_ = projects_[newProject_].id;
  loadSandboxes();
  snprintf(detailName_, sizeof(detailName_), "%s", created);
  sel_ = top_ = 0;
  go(V_DETAIL);
}

// The list shows the sandbox as being removed until the Mac finishes.
void App::deleteSandbox() {
  busy("Deleting the sandbox");
  if (!api_.remove(detailName_)) {
    notice(api_.error(), V_DETAIL);
    return;
  }
  loadSandboxes();
  sel_ = top_ = 0;
  go(V_SANDBOXES);
}

void App::sandboxAction(const char *verb) {
  busy("Working");
  if (!api_.action(detailName_, verb)) {
    notice(api_.error(), V_DETAIL);
    return;
  }
  loadSandboxes();
  go(V_DETAIL);
}

// ---------------------------------------------------------------------------
// Terminal
// ---------------------------------------------------------------------------

void App::vtReply(void *ctx, const uint8_t *data, size_t len) {
  static_cast<App *>(ctx)->ws_.sendBinary(data, len);
}

void App::attach(const char *name, bool shell) {
  if (name != termName_) snprintf(termName_, sizeof(termName_), "%s", name);
  termShell_ = shell;
  busy(shell ? "Opening a shell" : "Attaching");
  char suffix[48], path[128];
  snprintf(suffix, sizeof(suffix), "/terminal?cols=%d&rows=%d%s", gridCols, termRows, shell ? "&shell=1" : "");
  sandboxPath(path, sizeof(path), name, suffix);
  if (!ws_.open(config_, path)) {
    snprintf(closedText_, sizeof(closedText_), "%s", ws_.error());
    canReattach_ = ws_.closeCode() != 4001;
    go(V_CLOSED);
    return;
  }
  vt_.reset();
  scrollDepth_ = 0;
  menuOpen_ = false;
  request_ = REQ_NONE;
  termOpen_ = true;
  blinkOn_ = true;
  lastBlink_ = p_.ms();
  drawnCursorRow_ = drawnCursorCol_ = -1;
  view_ = V_TERMINAL;
  needsDraw_ = false;
}

void App::pumpTerminal() {
  // Bounded, so keys and the screen get a turn during a flood of output.
  for (int i = 0; i < 4; i++) {
    const int n = ws_.poll(termBuf_, sizeof(termBuf_));
    if (n < 0) {
      terminalClosed();
      return;
    }
    if (n > 0) vt_.feed(termBuf_, n);
    if (char *json = ws_.text()) {
      handleServerMessage(json);
      ws_.clearText();
      if (reviewReady_) return;  // tick leaves the terminal for the form
      continue;
    }
    if (n == 0) break;
  }
}

// ---------------------------------------------------------------------------
// Dictation
// ---------------------------------------------------------------------------

void App::dictationStatus(const char *message) {
  snprintf(dictationStatus_, sizeof(dictationStatus_), "%s", message);
  statusUntil_ = p_.ms() + STATUS_MS;
}

void App::endMic() {
  if (dictation_ == DICT_RECORDING) p_.micStop();
  mic_.end();
}

// The terminal's socket carries the audio. A form has none, so it opens one for each recording;
// the Wio has memory for only one TLS connection, so it's closed again before anything else.
void App::dictationLost() {
  if (view_ == V_TERMINAL) {
    terminalClosed();
    return;
  }
  endMic();
  dictation_ = DICT_IDLE;
  ws_.close();
  dictationStatus("Lost the connection");
  needsDraw_ = true;
}

void App::startDictation() {
  const bool form = view_ != V_TERMINAL;
  if (!typeTranscript(sizeof(transcript_))) {  // anything still being typed goes first
    terminalClosed();
    return;
  }
  dictationStatus_[0] = 0;
  if (form) {
    dictationStatus("Connecting");
    drawFormFooter();
    flush();
    const bool opened = ws_.open(config_, "/v1/dictate");
    dictationStatus_[0] = 0;
    if (!opened) {
      dictationStatus(ws_.error());
      needsDraw_ = true;
      return;
    }
  }
  if (!mic_.begin()) {
    if (form) ws_.close();
    dictationStatus("Not enough memory for the mic");
    needsDraw_ = true;
    return;
  }
  if (!p_.micStart(mic_)) {
    mic_.end();
    if (form) ws_.close();
    dictationStatus("Couldn't start the mic");
    needsDraw_ = true;
    return;
  }
  dictation_ = DICT_RECORDING;
  dictationAt_ = p_.ms();
  needsDraw_ = form;
  if (!ws_.sendText("{\"type\":\"dictate-start\"}")) dictationLost();
}

void App::finishDictation() {
  p_.micStop();
  // The interrupt has stopped, so what's buffered is all there is.
  for (;;) {
    const size_t n = mic_.read(audioChunk_, sizeof(audioChunk_));
    if (!n) break;
    if (!ws_.sendAudio(audioChunk_, n)) {
      mic_.end();
      dictation_ = DICT_IDLE;
      dictationLost();
      return;
    }
  }
  mic_.end();
  dictation_ = DICT_WAITING;
  dictationAt_ = p_.ms();
  if (view_ != V_TERMINAL) needsDraw_ = true;
  if (!ws_.sendText("{\"type\":\"dictate-end\"}")) {
    dictation_ = DICT_IDLE;
    dictationLost();
  }
}

void App::cancelDictation() {
  endMic();
  dictation_ = DICT_IDLE;
  dictationStatus("Dictation cancelled");
  if (view_ != V_TERMINAL) {
    ws_.close();  // which also stops the Mac transcribing
    needsDraw_ = true;
  } else if (!ws_.sendText("{\"type\":\"dictate-cancel\"}")) {
    terminalClosed();
  }
}

void App::pumpDictation() {
  if (request_ != REQ_NONE && p_.ms() - requestAt_ > REQUEST_WAIT_MS) {
    request_ = REQ_NONE;
    dictationStatus("The Mac didn't answer");
  }
  if (dictation_ == DICT_WAITING && p_.ms() - dictationAt_ > TRANSCRIBE_WAIT_MS) {
    dictation_ = DICT_IDLE;
    dictationStatus("The Mac didn't answer");
    if (view_ != V_TERMINAL) {
      ws_.close();
      needsDraw_ = true;
    }
    return;
  }
  if (dictation_ != DICT_RECORDING) return;
  if (mic_.overflowed() || p_.ms() - dictationAt_ >= MAX_RECORDING_MS) {
    finishDictation();
    return;
  }
  // Whole chunks, so a slow moment on the network is absorbed by the mic's buffer.
  while (mic_.available() >= sizeof(audioChunk_)) {
    const size_t n = mic_.read(audioChunk_, sizeof(audioChunk_));
    if (!ws_.sendAudio(audioChunk_, n)) {
      endMic();
      dictation_ = DICT_IDLE;
      dictationLost();
      return;
    }
  }
}

void App::handleServerMessage(char *json) {
  JsonDocument doc;
  if (deserializeJson(doc, json)) return;
  const char *type = doc["type"] | "";
  if (!strcmp(type, "reply") || !strcmp(type, "review") || !strcmp(type, "reply-error")) {
    const MenuRequest asked = request_;
    if (asked == REQ_NONE) return;  // it took too long, and the Wio has moved on
    request_ = REQ_NONE;
    if (!strcmp(type, "reply-error")) {
      dictationStatus(doc["message"] | "Couldn't read the reply");
    } else if (asked == REQ_COPY && !strcmp(type, "reply")) {
      // Control characters are dropped here too: this gets typed into a live terminal.
      size_t n = 0;
      for (const char *t = doc["text"] | ""; *t && n + 1 < sizeof(clipboard_); t++) {
        const uint8_t c = (uint8_t)*t;
        if (c >= 0x20 && c != 0x7F) clipboard_[n++] = *t;
      }
      clipboard_[n] = 0;
      const unsigned copied = doc["copied"] | 0, total = doc["total"] | 0;
      char status[32];
      if (copied < total) snprintf(status, sizeof(status), "Copied %u of %u chars", copied, total);
      else snprintf(status, sizeof(status), "Copied %u chars", copied);
      dictationStatus(status);
    } else if (asked == REQ_REVIEW && !strcmp(type, "review")) {
      if (dictation_ != DICT_IDLE) {
        dictationStatus("Finish dictating first");
      } else {
        snprintf(reviewUrl_, sizeof(reviewUrl_), "%s", doc["url"] | "");
        reviewProject_ = doc["projectId"] | (int64_t)-1;
        reviewReady_ = reviewUrl_[0] != 0;
      }
    }
    return;
  }
  if (dictation_ != DICT_WAITING) return;  // cancelled while it was on its way
  if (!strcmp(type, "dictate-error")) {
    dictation_ = DICT_IDLE;
    dictationStatus(doc["message"] | "Dictation failed");
    if (view_ != V_TERMINAL) {
      ws_.close();
      needsDraw_ = true;
    }
  } else if (!strcmp(type, "transcript")) {
    dictation_ = DICT_IDLE;
    // Control characters are dropped here too: this is typed into a live terminal.
    size_t n = 0;
    for (const char *t = doc["text"] | ""; *t && n + 2 < sizeof(transcript_); t++) {
      const uint8_t c = (uint8_t)*t;
      if (c >= 0x20 && c != 0x7F) transcript_[n++] = *t;
    }
    if (view_ != V_TERMINAL) {
      transcript_[n] = 0;
      insertTranscript(transcript_);
      ws_.close();
      needsDraw_ = true;
      return;
    }
    if (n) transcript_[n++] = ' ';  // so the next dictation continues the sentence
    transcriptPos_ = 0;
    transcriptLen_ = n;
  }
}

void App::pumpFormDictation() {
  const int n = ws_.poll(termBuf_, sizeof(termBuf_));
  if (char *json = ws_.text()) {
    handleServerMessage(json);
    ws_.clearText();
    return;
  }
  if (n < 0) dictationLost();
}

// The form's voice controls. Returns true if the input was theirs. While dictating, other keys
// are held back so the words can't land in a different field.
bool App::handleFormVoice(const Input &in) {
  if (dictation_ != DICT_IDLE) {
    if (in.key == BTN_A && dictation_ == DICT_RECORDING) finishDictation();
    else if (in.key == BTN_B || in.key == BTN_B_HOLD || isBack(in.key)) cancelDictation();
    return true;
  }
  if (in.key == BTN_A) {
    if (aPending_) {
      aPending_ = false;
      handleForm(Input{KEY_ENTER, 0, 0});
    } else {
      aPending_ = true;
      aAt_ = p_.ms();
    }
    return true;
  }
  if (in.key == BTN_B_HOLD) {
    Field &f = fields_[focus_];
    if (f.type == F_TEXT) {
      f.text[0] = 0;
      textCursor_ = 0;
      needsDraw_ = true;
    }
    return true;
  }
  return false;
}

// Types what's left of the transcript, up to maxBytes, without pressing Enter. Returns false if
// the connection is gone.
bool App::typeTranscript(size_t maxBytes) {
  size_t n = transcriptLen_ - transcriptPos_;
  if (!n) return true;
  if (n > maxBytes) {
    n = maxBytes;
    // Whole UTF-8 characters, in case the far end reads them a frame at a time.
    while (n > 1 && ((uint8_t)transcript_[transcriptPos_ + n] & 0xC0) == 0x80) n--;
  }
  if (!ws_.sendBinary((const uint8_t *)transcript_ + transcriptPos_, n)) {
    transcriptPos_ = transcriptLen_ = 0;
    return false;
  }
  transcriptPos_ += n;
  return true;
}

void App::terminalClosed() {
  menuOpen_ = false;
  request_ = REQ_NONE;
  endMic();
  dictation_ = DICT_IDLE;
  transcriptPos_ = transcriptLen_ = 0;
  termOpen_ = false;
  const uint16_t code = ws_.closeCode();
  if (ws_.closeReason()[0]) {
    snprintf(closedText_, sizeof(closedText_), "%s", ws_.closeReason());
  } else if (code == 1000) {
    snprintf(closedText_, sizeof(closedText_), "Detached. The agent keeps running.");
  } else {
    snprintf(closedText_, sizeof(closedText_), "Lost the connection to the Mac.");
  }
  canReattach_ = code != 4001;
  ws_.close();
  vt_.reset();
  go(V_CLOSED);
}

void App::detach() {
  menuOpen_ = false;
  request_ = REQ_NONE;
  endMic();
  dictation_ = DICT_IDLE;
  transcriptPos_ = transcriptLen_ = 0;
  termOpen_ = false;
  ws_.close();
  vt_.reset();
  loadSandboxes();
  go(V_DETAIL);
}

// tmux has the mouse on, so the wheel opens its copy mode and scrolls its 50,000 lines of history.
// Scrolling back to the bottom leaves copy mode by itself.
void App::wheel(bool up, int notches) {
  if (!vt_.mouseOn()) return;
  char seq[24];
  const int button = up ? 64 : 65;
  const int col = gridCols / 2, row = termRows / 2;
  int len;
  if (vt_.mouseSgr()) {
    len = snprintf(seq, sizeof(seq), "\x1b[<%d;%d;%dM", button, col, row);
  } else {
    seq[0] = 0x1B;
    seq[1] = '[';
    seq[2] = 'M';
    seq[3] = (char)(32 + button);
    seq[4] = (char)(32 + col);
    seq[5] = (char)(32 + row);
    len = 6;
  }
  for (int i = 0; i < notches; i++) ws_.sendBinary((const uint8_t *)seq, len);
  scrollDepth_ += up ? notches : -notches;
  if (scrollDepth_ < 0) scrollDepth_ = 0;
}

void App::openMenu() {
  menuOpen_ = menuDirty_ = true;
  menuSel_ = 0;
}

void App::closeMenu() {
  menuOpen_ = false;
  // The session kept drawing underneath.
  for (int r = gridRows - MENU_ROWS; r < gridRows; r++) vt_.grid()->dirty |= 1UL << r;
}

void App::handleMenu(const Input &in) {
  if (isUp(in.key)) {
    menuSel_ = (menuSel_ + MENU_ITEMS - 1) % MENU_ITEMS;
    menuDirty_ = true;
  } else if (isDown(in.key)) {
    menuSel_ = (menuSel_ + 1) % MENU_ITEMS;
    menuDirty_ = true;
  } else if (isBack(in.key) || in.key == SW_LEFT) {
    closeMenu();
  } else if (isSelect(in.key) || in.key == SW_RIGHT) {
    const uint8_t chosen = menuSel_;
    closeMenu();
    if (chosen == 2) pasteReply();
    else requestReply(chosen == 0);
  }
}

void App::drawMenu() {
  menuDirty_ = false;
  Cell line[MAX_COLS];
  const int top = gridRows - MENU_ROWS;
  for (int i = 0; i < MENU_ROWS; i++) {
    const bool on = i > 0 && i - 1 == menuSel_;
    const uint16_t bg = on ? C_SELECT : C_PANEL;
    const bool dim = i == MENU_ITEMS && !clipboard_[0];
    const uint16_t fg = i == 0 ? C_ACCENT : dim ? C_MUTED : C_TEXT;
    const char *s = i == 0 ? "Actions  Press select  C close" : MENU_LABELS[i - 1];
    for (int c = 0; c < gridCols; c++) line[c] = Cell{' ', fg, bg, 0};
    for (int c = 0; s[c] && c + 1 < gridCols; c++) line[c + 1].ch = (uint8_t)s[c];
    renderRow(line, -1, strip_);
    p_.pushStrip(screenTop + (top + i) * stripH, stripH, strip_);
  }
}

void App::requestReply(bool review) {
  if (!ws_.sendText(review ? "{\"type\":\"reply-review\"}" : "{\"type\":\"reply-copy\"}")) {
    terminalClosed();
    return;
  }
  request_ = review ? REQ_REVIEW : REQ_COPY;
  requestAt_ = p_.ms();
  dictationStatus(review ? "Finding the PR" : "Copying the reply");
}

// Types the copied reply at the prompt, at dictation's pace and without pressing Enter.
void App::pasteReply() {
  if (!clipboard_[0]) {
    dictationStatus("Nothing copied yet");
    return;
  }
  if (!typeTranscript(sizeof(transcript_))) {  // anything still being typed goes first
    terminalClosed();
    return;
  }
  const size_t n = strlen(clipboard_);
  memcpy(transcript_, clipboard_, n);
  transcriptPos_ = 0;
  transcriptLen_ = n;
  dictationStatus("Pasted");
}

// A new sandbox on the PR, with the Create button ready. With no task, the Mac asks for a review.
void App::openReviewForm() {
  int index = -1;
  for (int i = 0; i < projectCount_; i++) {
    if (projects_[i].id == reviewProject_) index = i;
  }
  detach();
  dictationStatus_[0] = 0;  // "Finding the PR" would otherwise hide the form's hints
  if (index < 0) {
    notice("This sandbox's project isn't loaded. Refresh the projects and try again.", V_DETAIL);
    return;
  }
  openNewForm(index, reviewUrl_);
  if (view_ != V_NEW) return;
  for (int i = 0; i < fieldCount_; i++) {
    if (fields_[i].id == FIELD_CREATE) focus_ = i;
  }
  textCursor_ = 0;
  needsDraw_ = true;
}

void App::handleTerminal(const Input &in) {
  if (menuOpen_) {
    handleMenu(in);
    return;
  }
  switch (in.key) {
    case BTN_C:
      detach();
      return;
    case BTN_B:
      if (dictation_ != DICT_IDLE) {
        cancelDictation();  // this tap can't be the first of a double tap
      } else if (p_.keyboard() == KB_LOST) {
        p_.restart();
      } else if (!termShell_) {
        // A single tap does nothing here, so there's no need to wait to see if a second follows.
        if (bPending_ && p_.ms() - bAt_ <= DOUBLE_TAP_MS) {
          bPending_ = false;
          handleTerminal(Input{KEY_ESC, 0, 0});
        } else {
          bPending_ = true;
          bAt_ = p_.ms();
        }
      }
      return;
    case BTN_B_HOLD:
      if (dictation_ != DICT_IDLE) {
        cancelDictation();
      } else {
        transcriptPos_ = transcriptLen_ = 0;  // what wasn't typed yet is cleared too
        const uint8_t ctrlC = 0x03;
        if (!ws_.sendBinary(&ctrlC, 1)) terminalClosed();
        else dictationStatus("Sent Ctrl+C");
      }
      return;
    case BTN_A:
      if (dictation_ == DICT_RECORDING) {
        finishDictation();
      } else if (dictation_ == DICT_IDLE) {
        // One tap records, after a moment to see whether a second is coming.
        if (aPending_) {
          aPending_ = false;
          handleTerminal(Input{KEY_ENTER, 0, 0});
        } else {
          aPending_ = true;
          aAt_ = p_.ms();
        }
      }
      return;
    case SW_UP:
      wheel(true, 1);
      return;
    case SW_DOWN:
      wheel(false, 1);
      return;
    case SW_LEFT:
      wheel(true, PAGE_NOTCHES);
      return;
    case SW_RIGHT:
      wheel(false, PAGE_NOTCHES);
      return;
    case SW_PRESS:
      // Back to the live screen. Extra wheel events past the bottom do nothing.
      if (scrollDepth_ > 0) {
        wheel(false, scrollDepth_ + 1);
        scrollDepth_ = 0;
      } else if (dictation_ == DICT_IDLE && request_ == REQ_NONE) {
        openMenu();
      }
      return;
    default:
      break;
  }
  // A key pressed while the transcript is being typed lands after it.
  if (!typeTranscript(sizeof(transcript_))) {
    terminalClosed();
    return;
  }
  char bytes[16];
  const size_t n = keyToBytes(in, vt_.appCursor(), bytes);
  if (n && !ws_.sendBinary((const uint8_t *)bytes, n)) terminalClosed();
}

void App::drawTerminalBar() {
  Grid *g = vt_.grid();
  char left[MAX_COLS + 1];
  snprintf(left, sizeof(left), " %s%s", termName_, termShell_ ? " (shell)" : "");
  const KeyboardState kb = p_.keyboard();
  char recording[28];
  const char *right = kb == KB_LOST ? "KB LOST: B restarts " : scrollDepth_ > 0 ? "SCROLL: press=live " : "A mic AA=enter C back ";
  uint16_t fg = scrollDepth_ > 0 ? C_WARN : C_MUTED;
  if (dictation_ == DICT_RECORDING) {
    snprintf(recording, sizeof(recording), "REC %2us  A send B cancel ", (unsigned)((p_.ms() - dictationAt_) / 1000));
    right = recording;
    fg = C_FAIL;
  } else if (dictation_ == DICT_WAITING) {
    right = "Transcribing  B cancel ";
    fg = C_ACCENT;
  } else if (dictationStatus_[0]) {
    if ((int32_t)(p_.ms() - statusUntil_) < 0) {
      snprintf(recording, sizeof(recording), "%.26s ", dictationStatus_);
      right = recording;
      fg = C_WARN;
    } else {
      dictationStatus_[0] = 0;
    }
  }
  const int rightLen = strlen(right);
  const uint16_t bg = kb == KB_LOST ? 0x8000 : C_PANEL;
  for (int c = 0; c < gridCols; c++) g->put(0, c, ' ', C_TEXT, bg);
  const int leftMax = gridCols - rightLen - 1;
  for (int c = 0; c < leftMax && left[c]; c++) g->put(0, c, (uint8_t)left[c], C_ACCENT, bg);
  for (int c = 0; c < rightLen; c++) g->put(0, gridCols - rightLen + c, (uint8_t)right[c], kb == KB_LOST ? C_TEXT : fg, bg);
  // A level meter, so it's clear the mic hears you.
  if (dictation_ == DICT_RECORDING && gridCols - rightLen - 2 >= 0) {
    const uint8_t level = mic_.takeLevel();
    g->put(0, gridCols - rightLen - 2, level ? 0x2580 + level : ' ', C_OK, bg);
  }
}

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

void App::tick() {
  Input in;
  int handled = 0;
  while (handled++ < 8 && p_.poll(in)) handle(in);

  if (aPending_ && p_.ms() - aAt_ >= DOUBLE_TAP_MS) {
    aPending_ = false;
    if (view_ == V_TERMINAL && termOpen_) {
      startDictation();
    } else if (view_ == V_NEW) {
      if (fields_[focus_].type == F_TEXT) startDictation();
      else dictationStatus("Pick a text field to dictate");
      needsDraw_ = true;
    }
  }

  if (view_ == V_TERMINAL && termOpen_) pumpTerminal();
  if (reviewReady_) {
    reviewReady_ = false;
    if (view_ == V_TERMINAL && termOpen_) openReviewForm();
  }
  if (view_ == V_TERMINAL && termOpen_) pumpDictation();
  if (view_ == V_NEW && dictation_ != DICT_IDLE) {
    pumpFormDictation();
    if (dictation_ != DICT_IDLE) pumpDictation();
  }
  if (view_ == V_NEW && (dictation_ != DICT_IDLE || dictationStatus_[0]) && p_.ms() - lastFooter_ >= 200) {
    lastFooter_ = p_.ms();
    drawFormFooter();
  }

  if (view_ == V_TERMINAL) {
    const uint32_t now = p_.ms();
    if (termOpen_ && transcriptPos_ < transcriptLen_ && now - lastTyped_ >= TYPE_EVERY_MS) {
      lastTyped_ = now;
      if (!typeTranscript(TYPE_BYTES)) {
        terminalClosed();
        return;
      }
    }
    if (now - lastBlink_ >= BLINK_MS) {
      blinkOn_ = !blinkOn_;
      lastBlink_ = now;
      vt_.grid()->dirty |= 1UL << (vt_.cursorRow() + 1);
    }
    if (now - lastFlush_ >= FLUSH_MS) {
      drawTerminalBar();
      flush();
      lastFlush_ = now;
    }
    return;
  }
  if (needsDraw_) {
    draw();
    needsDraw_ = false;
  }
  flush();
}

void App::handle(const Input &in) {
  if (in.key != BTN_A) aPending_ = false;
  if (in.key != BTN_B) bPending_ = false;
  if (view_ == V_TERMINAL) {
    handleTerminal(in);
    return;
  }
  switch (view_) {
    case V_WIFI:
      if (in.key == BTN_A || (in.key == KEY_TEXT && in.ch == 'r')) {
        scanWifi();
        return;
      }
      handleList(in, networkCount_);
      break;
    case V_PROJECTS:
      if (in.key == BTN_A || (in.key == KEY_TEXT && in.ch == 'r')) {
        loadHome();
        return;
      }
      if (in.key == BTN_B || (in.key == KEY_TEXT && in.ch == 'n')) {
        openNewForm(sel_ > 0 && sel_ <= projectCount_ ? sel_ - 1 : 0);
        return;
      }
      handleList(in, projectCount_ + 3);
      break;
    case V_SANDBOXES:
      if (in.key == BTN_A || (in.key == KEY_TEXT && in.ch == 'r')) {
        loadSandboxes();
        go(V_SANDBOXES);
        return;
      }
      if (in.key == BTN_B || (in.key == KEY_TEXT && in.ch == 'n')) {
        int index = 0;
        for (int i = 0; i < projectCount_; i++) {
          if (projects_[i].id == filterProject_) index = i;
        }
        openNewForm(index);
        return;
      }
      handleList(in, visibleSandboxCount());
      break;
    case V_DETAIL: {
      // Delete needs a second press, and anything else in between cancels it.
      const bool armed = deleteArmed_;
      deleteArmed_ = false;
      if (armed) needsDraw_ = true;
      if (in.key == BTN_A || (in.key == KEY_TEXT && in.ch == 'r')) {
        loadSandboxes();
        go(V_DETAIL);
        return;
      }
      if (isSelect(in.key) && sel_ == DETAIL_DELETE && !armed) {
        deleteArmed_ = true;
        return;
      }
      handleList(in, DETAIL_ACTIONS);
      break;
    }
    case V_SETTINGS:
      if (sel_ == 0 && (in.key == KEY_LEFT || in.key == SW_LEFT)) {
        changeFont(-1);
      } else if (sel_ == 0 && (in.key == KEY_RIGHT || in.key == SW_RIGHT || isSelect(in.key))) {
        changeFont(1);
      } else {
        handleList(in, 5);
      }
      break;
    case V_NEW:
      if (!handleFormVoice(in)) handleForm(in);
      break;
    case V_WIFI_PASS:
    case V_SERVER:
      handleForm(in);
      break;
    case V_CONFIRM:
      if (isSelect(in.key)) finishPairing();
      else if (isBack(in.key)) {
        buildServerForm();
        go(V_SERVER);
      }
      break;
    case V_CLOSED:
      if (isSelect(in.key) && canReattach_) {
        attach(termName_, termShell_);
      } else if (isBack(in.key) || isSelect(in.key)) {
        loadSandboxes();
        go(V_DETAIL);
      }
      break;
    case V_NOTICE:
      if (isSelect(in.key) || isBack(in.key)) go(noticeBack_);
      break;
    default:
      break;
  }
}

void App::handleList(const Input &in, int count) {
  if (isUp(in.key)) {
    if (sel_ > 0) sel_--;
    needsDraw_ = true;
  } else if (isDown(in.key)) {
    if (sel_ < count - 1) sel_++;
    needsDraw_ = true;
  } else if (in.key == KEY_PGUP || in.key == SW_LEFT) {
    if (in.key == SW_LEFT && view_ != V_WIFI) {
      goBack();
      return;
    }
    sel_ = sel_ > 8 ? sel_ - 8 : 0;
    needsDraw_ = true;
  } else if (in.key == KEY_PGDN) {
    sel_ = sel_ + 8 < count ? sel_ + 8 : (count > 0 ? count - 1 : 0);
    needsDraw_ = true;
  } else if (isSelect(in.key) || in.key == SW_RIGHT) {
    if (count > 0) activate();
  } else if (isBack(in.key)) {
    goBack();
  }
}

void App::goBack() {
  switch (view_) {
    case V_SANDBOXES:
    case V_SETTINGS:
      sel_ = top_ = 0;
      go(V_PROJECTS);
      break;
    case V_DETAIL:
      sel_ = top_ = 0;
      go(V_SANDBOXES);
      break;
    case V_NEW:
      sel_ = top_ = 0;
      go(filterProject_ >= 0 ? V_SANDBOXES : V_PROJECTS);
      break;
    case V_WIFI_PASS:
      sel_ = top_ = 0;
      go(V_WIFI);
      break;
    case V_SERVER:
      if (config_.paired) {
        sel_ = top_ = 0;
        go(V_SETTINGS);
      }
      break;
    case V_WIFI:
      if (config_.paired && p_.wifiUp()) {
        sel_ = top_ = 0;
        go(V_SETTINGS);
      }
      break;
    default:
      break;
  }
}

void App::activate() {
  switch (view_) {
    case V_WIFI: {
      const WifiNetwork &n = networks_[sel_];
      snprintf(config_.ssid, sizeof(config_.ssid), "%s", n.ssid);
      if (n.open) {
        config_.pass[0] = 0;
        joinWifi();
      } else {
        passText_[0] = 0;
        formError_[0] = 0;
        buildPassForm();
        go(V_WIFI_PASS);
      }
      break;
    }
    case V_PROJECTS:
      if (sel_ == 0) {
        filterProject_ = -1;
      } else if (sel_ <= projectCount_) {
        filterProject_ = projects_[sel_ - 1].id;
      } else if (sel_ == projectCount_ + 1) {
        openNewForm(0);
        return;
      } else {
        sel_ = top_ = 0;
        go(V_SETTINGS);
        return;
      }
      sel_ = top_ = 0;
      go(V_SANDBOXES);
      break;
    case V_SANDBOXES: {
      const int i = visibleSandbox(sel_);
      if (i < 0) return;
      snprintf(detailName_, sizeof(detailName_), "%s", sandboxes_[i].name);
      sel_ = top_ = 0;
      go(V_DETAIL);
      break;
    }
    case V_DETAIL: {
      SandboxInfo *s = selectedSandbox();
      if (!s) {
        notice("That sandbox is gone.", V_SANDBOXES);
        return;
      }
      if (sel_ == 0) attach(s->name, false);
      else if (sel_ == 1) attach(s->name, true);
      else if (sel_ == 2) sandboxAction(s->running ? "stop" : "resume");
      else deleteSandbox();
      break;
    }
    case V_SETTINGS:
      if (sel_ == 1) {
        scanWifi();
      } else if (sel_ == 2) {
        startServerForm();
      } else if (sel_ == 3) {
        p_.forgetKeyboards();
        p_.restart();
      } else {
        p_.restart();
      }
      break;
    default:
      break;
  }
}

static void insertChar(char *buf, size_t cap, int &cursor, char c) {
  const size_t len = strlen(buf);
  if (len + 1 >= cap) return;
  memmove(buf + cursor + 1, buf + cursor, len - cursor + 1);
  buf[cursor++] = c;
}

static void removeBefore(char *buf, int &cursor) {
  if (cursor <= 0) return;
  memmove(buf + cursor - 1, buf + cursor, strlen(buf + cursor) + 1);
  cursor--;
}

// Puts spoken words in the focused field at its cursor. A task keeps them as a sentence. Branch
// and kit names become a-slug-like-this, and a pull request keeps only its number, since Whisper
// capitalizes and punctuates. Forms draw and send ASCII here.
void App::insertTranscript(const char *spoken) {
  Field &f = fields_[focus_];
  if (f.type != F_TEXT) return;
  const bool pr = f.id == FIELD_VALUE && sourceIndex_ == 1;
  if (f.id == FIELD_TASK) {
    if (textCursor_ > 0 && f.text[textCursor_ - 1] != ' ') insertChar(f.text, f.cap, textCursor_, ' ');
    for (const char *t = spoken; *t; t++) {
      if ((uint8_t)*t < 0x80) insertChar(f.text, f.cap, textCursor_, *t);
    }
    needsDraw_ = true;
    return;
  }
  const auto afterWord = [&] {
    return !pr && textCursor_ > 0 && f.text[textCursor_ - 1] != '-' && f.text[textCursor_ - 1] != '/';
  };
  bool wantDash = afterWord();
  for (const char *t = spoken; *t; t++) {
    char c = *t;
    if (c >= 'A' && c <= 'Z') c += 32;
    if (c == '/' && f.id == FIELD_VALUE && !pr) {  // branches like feature/login
      insertChar(f.text, f.cap, textCursor_, c);
      wantDash = false;
      continue;
    }
    const bool digit = c >= '0' && c <= '9';
    if (pr ? !digit : !(digit || (c >= 'a' && c <= 'z'))) {
      wantDash = afterWord();
      continue;
    }
    if (wantDash) insertChar(f.text, f.cap, textCursor_, '-');
    wantDash = false;
    insertChar(f.text, f.cap, textCursor_, c);
  }
  needsDraw_ = true;
}

void App::handleForm(const Input &in) {
  Field &f = fields_[focus_];
  const auto moveFocus = [&](int delta) {
    int next = focus_;
    do {
      next += delta;
    } while (next >= 0 && next < fieldCount_ && fields_[next].type == F_NOTE);
    if (next < 0 || next >= fieldCount_) return;
    focus_ = next;
    textCursor_ = fields_[focus_].type == F_TEXT ? strlen(fields_[focus_].text) : 0;
    needsDraw_ = true;
  };

  if (isBack(in.key)) {
    goBack();
    return;
  }
  if (isUp(in.key)) {
    moveFocus(-1);
    return;
  }
  if (isDown(in.key) || (in.key == KEY_TAB && !(in.mods & MOD_SHIFT))) {
    moveFocus(1);
    return;
  }
  if (in.key == KEY_TAB) {
    moveFocus(-1);
    return;
  }

  if (f.type == F_TEXT) {
    needsDraw_ = true;
    switch (in.key) {
      case KEY_TEXT:
        if (in.mods & (MOD_CTRL | MOD_GUI)) return;
        insertChar(f.text, f.cap, textCursor_, in.ch);
        return;
      case KEY_BACKSPACE:
        removeBefore(f.text, textCursor_);
        return;
      case KEY_DELETE:
        if (f.text[textCursor_]) {
          textCursor_++;
          removeBefore(f.text, textCursor_);
        }
        return;
      case KEY_LEFT:
      case SW_LEFT:
        if (textCursor_ > 0) textCursor_--;
        return;
      case KEY_RIGHT:
      case SW_RIGHT:
        if (f.text[textCursor_]) textCursor_++;
        return;
      case KEY_HOME:
        textCursor_ = 0;
        return;
      case KEY_END:
        textCursor_ = strlen(f.text);
        return;
      case KEY_ENTER:
      case SW_PRESS:
        if (f.id == FIELD_ADD_KIT && f.text[0]) addKit();
        else moveFocus(1);
        return;
      default:
        return;
    }
  }
  if (f.type == F_CHOICE) {
    if (in.key == KEY_LEFT || in.key == SW_LEFT) changeChoice(f, -1);
    else if (in.key == KEY_RIGHT || in.key == SW_RIGHT || isSelect(in.key) ||
             (in.key == KEY_TEXT && in.ch == ' ')) {
      changeChoice(f, 1);
    }
    return;
  }
  if (f.type == F_TOGGLE) {
    if (isSelect(in.key) || in.key == KEY_LEFT || in.key == KEY_RIGHT || in.key == SW_LEFT ||
        in.key == SW_RIGHT || (in.key == KEY_TEXT && in.ch == ' ')) {
      toggleField(f);
    }
    return;
  }
  if (f.type == F_BUTTON && isSelect(in.key)) {
    formError_[0] = 0;
    if (f.id == FIELD_CREATE) {
      submitNew();
    } else if (view_ == V_WIFI_PASS) {
      snprintf(config_.pass, sizeof(config_.pass), "%s", passText_);
      joinWifi();
    } else {
      probeServer();
    }
  }
}

// ---------------------------------------------------------------------------
// Drawing
// ---------------------------------------------------------------------------

void App::go(View v) {
  view_ = v;
  needsDraw_ = true;
  draw();
  needsDraw_ = false;
  flush();
}

void App::clearUi() {
  for (int r = 0; r < gridRows; r++) {
    for (int c = 0; c < gridCols; c++) gridA_.put(r, c, ' ', C_TEXT, C_BG);
  }
}

// Draws UTF-8 text and returns the column after it.
int App::text(int row, int col, const char *s, uint16_t fg, uint16_t bg, int maxCols) {
  const uint8_t *u = (const uint8_t *)s;
  int used = 0;
  while (*u && used < maxCols && col + used < gridCols) {
    uint32_t cp = *u++;
    int more = cp >= 0xF0 ? 3 : cp >= 0xE0 ? 2 : cp >= 0xC0 ? 1 : 0;
    if (more) {
      cp &= 0x3F >> more;
      while (more-- && (*u & 0xC0) == 0x80) cp = cp << 6 | (*u++ & 0x3F);
    }
    const int w = charWidth(cp);
    if (w == 0) continue;
    if (used + w > maxCols) break;
    gridA_.put(row, col + used, cp > 0xFFFF ? 0xFFFD : (uint16_t)cp, fg, bg);
    if (w == 2) gridA_.put(row, col + used + 1, ' ', fg, bg, CELL_WIDE_TAIL);
    used += w;
  }
  return col + used;
}

void App::header(const char *title) {
  for (int c = 0; c < gridCols; c++) gridA_.put(0, c, ' ', C_TEXT, C_PANEL);
  const KeyboardState kb = p_.keyboard();
  const char *status = kb == KB_CONNECTED ? "KB" : kb == KB_LOST ? "KB LOST" : "no KB";
  const int len = strlen(status);
  text(0, 1, title, C_ACCENT, C_PANEL, gridCols - len - 3);
  text(0, gridCols - len - 1, status, kb == KB_CONNECTED ? C_OK : C_WARN, C_PANEL, len);
}

void App::footer(const char *hints) {
  for (int c = 0; c < gridCols; c++) gridA_.put(gridRows - 1, c, ' ', C_MUTED, C_PANEL);
  text(gridRows - 1, 1, hints, C_MUTED, C_PANEL, gridCols - 2);
}

void App::busy(const char *message) {
  if (view_ == V_TERMINAL) view_ = V_NOTICE;
  clearUi();
  header("sbx-projects");
  const int len = strlen(message);
  text(gridRows / 2, len < gridCols ? (gridCols - len) / 2 : 0, message, C_TEXT, C_BG, gridCols);
  flush();
}

void App::notice(const char *message, View back) {
  snprintf(noticeText_, sizeof(noticeText_), "%s", message);
  noticeBack_ = back;
  go(V_NOTICE);
}

void App::flush() {
  Grid *g = activeGrid();
  const bool term = view_ == V_TERMINAL;
  const int cursorRow = term && vt_.cursorVisible() ? vt_.cursorRow() + 1 : -1;
  const int cursorCol = vt_.cursorCol();
  if (term && (drawnCursorRow_ != cursorRow || drawnCursorCol_ != cursorCol)) {
    if (drawnCursorRow_ >= 0) g->dirty |= 1UL << drawnCursorRow_;
    if (cursorRow >= 0) g->dirty |= 1UL << cursorRow;
    drawnCursorRow_ = cursorRow;
    drawnCursorCol_ = cursorCol;
  }
  uint32_t bits = g->dirty;
  g->dirty = 0;
  for (int r = 0; bits; r++, bits >>= 1) {
    if (!(bits & 1)) continue;
    if (term && menuOpen_ && r >= gridRows - MENU_ROWS) continue;
    renderRow(g->cells[r], r == cursorRow && blinkOn_ ? cursorCol : -1, strip_);
    p_.pushStrip(screenTop + r * stripH, stripH, strip_);
  }
  if (term && menuOpen_ && menuDirty_) drawMenu();
}

void App::draw() {
  if (view_ == V_TERMINAL) return;
  clearUi();
  switch (view_) {
    case V_WIFI: drawWifi(); break;
    case V_WIFI_PASS: drawForm(config_.ssid); break;
    case V_SERVER: drawForm("Pair with your Mac"); break;
    case V_CONFIRM: drawConfirm(); break;
    case V_PROJECTS: drawProjects(); break;
    case V_SANDBOXES: drawSandboxes(); break;
    case V_DETAIL: drawDetail(); break;
    case V_NEW: drawForm("New sandbox"); break;
    case V_CLOSED: drawClosed(); break;
    case V_SETTINGS: drawSettings(); break;
    case V_NOTICE: drawNotice(); break;
    default: break;
  }
}

static void keepVisible(int sel, int &top, int rows) {
  if (sel < top) top = sel;
  if (sel >= top + rows) top = sel - rows + 1;
  if (top < 0) top = 0;
}

void App::drawWifi() {
  header("Choose Wi-Fi");
  footer("Enter join  A rescan");
  if (networkCount_ == 0) {
    text(3, 2, "No networks found. Press A to look again.", C_MUTED, C_BG, gridCols - 4);
    return;
  }
  const int rows = LIST_BOTTOM - LIST_TOP;
  keepVisible(sel_, top_, rows);
  for (int i = top_; i < networkCount_ && i < top_ + rows; i++) {
    const int row = LIST_TOP + 1 + i - top_;
    const bool on = i == sel_;
    const uint16_t bg = on ? C_SELECT : C_BG;
    for (int c = 0; c < gridCols; c++) gridA_.put(row, c, ' ', C_TEXT, bg);
    text(row, 1, networks_[i].ssid, C_TEXT, bg, gridCols - 11);
    char right[16];
    snprintf(right, sizeof(right), "%s%4d", networks_[i].open ? "open" : "", networks_[i].rssi);
    text(row, gridCols - 1 - strlen(right), right, C_MUTED, bg, 12);
  }
}

void App::drawProjects() {
  header("Projects");
  footer("Enter open  B new  A refresh");
  const int count = projectCount_ + 3;
  if (sel_ >= count) sel_ = count - 1;
  const int rows = LIST_BOTTOM - LIST_TOP;
  keepVisible(sel_, top_, rows);
  for (int i = top_; i < count && i < top_ + rows; i++) {
    const int row = LIST_TOP + 1 + i - top_;
    const bool on = i == sel_;
    const uint16_t bg = on ? C_SELECT : C_BG;
    for (int c = 0; c < gridCols; c++) gridA_.put(row, c, ' ', C_TEXT, bg);
    if (i == 0) {
      text(row, 1, "All sandboxes", C_ACCENT, bg, 40);
      char n[8];
      snprintf(n, sizeof(n), "%d", sandboxCount_);
      text(row, gridCols - 1 - strlen(n), n, C_MUTED, bg, 6);
    } else if (i <= projectCount_) {
      const ProjectInfo &pr = projects_[i - 1];
      const int after = text(row, 1, pr.name, C_TEXT, bg, gridCols - 6);
      text(row, after + 1, pr.branch, C_MUTED, bg, gridCols - after - 6);
      char n[8];
      snprintf(n, sizeof(n), "%d", pr.sandboxes);
      text(row, gridCols - 1 - strlen(n), n, C_MUTED, bg, 6);
    } else if (i == projectCount_ + 1) {
      text(row, 1, "+ New sandbox", C_OK, bg, 40);
    } else {
      text(row, 1, "Settings", C_MUTED, bg, 40);
    }
  }
}

void App::drawSandboxes() {
  const ProjectInfo *pr = filterProject_ >= 0 ? projectById(filterProject_) : nullptr;
  header(pr ? pr->name : "All sandboxes");
  footer("Enter  C back  B new  A reload");
  const int count = visibleSandboxCount();
  if (count == 0) {
    text(3, 2, "No sandboxes yet. Press B to make one.", C_MUTED, C_BG, gridCols - 4);
    return;
  }
  if (sel_ >= count) sel_ = count - 1;
  const int rows = (LIST_BOTTOM - LIST_TOP) / 2;
  keepVisible(sel_, top_, rows);
  for (int i = top_; i < count && i < top_ + rows; i++) {
    const SandboxInfo &s = sandboxes_[visibleSandbox(i)];
    const int row = LIST_TOP + 1 + (i - top_) * 2;
    const bool on = i == sel_;
    const uint16_t bg = on ? C_SELECT : C_BG;
    for (int c = 0; c < gridCols; c++) {
      gridA_.put(row, c, ' ', C_TEXT, bg);
      gridA_.put(row + 1, c, ' ', C_TEXT, bg);
    }
    gridA_.put(row, 1, s.running ? 0x25CF : 0x25CB, stateColor(s), bg);
    text(row, 3, s.title, C_TEXT, bg, gridCols - 7);
    if (s.unread) gridA_.put(row, gridCols - 2, 0x2022, C_ACCENT, bg);
    const int after = text(row + 1, 3, s.status, stateColor(s), bg, gridCols / 2);
    text(row + 1, after + 2, s.branch, C_MUTED, bg, gridCols - after - 3);
  }
}

void App::drawDetail() {
  SandboxInfo *s = selectedSandbox();
  if (!s) {
    header(detailName_);
    footer("C back");
    text(3, 2, "Starting. Press A to refresh.", C_MUTED, C_BG, gridCols - 4);
    return;
  }
  header(s->name);
  footer("Enter  C back  A refresh");
  int row = 1;
  text(row++, 1, s->title, C_TEXT, C_BG, gridCols - 2);
  const auto line = [&](const char *label, const char *value, uint16_t color) {
    text(row, 1, label, C_MUTED, C_BG, 8);
    text(row, 9, value, color, C_BG, gridCols - 10);
    row++;
  };
  line("Status", s->status, stateColor(*s));
  if (s->branch[0]) line("Branch", s->branch, C_TEXT);
  char agent[48];
  snprintf(agent, sizeof(agent), "%s%s%s", s->agent, s->model[0] ? " / " : "", s->model);
  line("Agent", agent, C_TEXT);
  char changes[32];
  snprintf(changes, sizeof(changes), "%u files  +%u -%u", s->files, s->added, s->removed);
  line("Changes", changes, C_TEXT);
  if (row + DETAIL_ACTIONS < gridRows - 1) row++;  // a gap, when the footer leaves room

  const char *actions[DETAIL_ACTIONS] = {"Attach to agent", "Open a shell",
                                         s->running ? "Stop sandbox" : "Resume sandbox",
                                         deleteArmed_ ? "Press again to delete" : "Delete sandbox"};
  for (int i = 0; i < DETAIL_ACTIONS; i++, row++) {
    const bool on = i == sel_;
    const uint16_t bg = on ? C_SELECT : C_BG;
    for (int c = 0; c < gridCols; c++) gridA_.put(row, c, ' ', C_TEXT, bg);
    const uint16_t color = i == 0 ? C_ACCENT : i == DETAIL_DELETE ? C_FAIL : C_TEXT;
    text(row, 2, actions[i], color, bg, gridCols - 4);
  }
}

void App::drawFormFooter() {
  const Field &focused = fields_[focus_];
  const bool voice = view_ == V_NEW;
  char line[MAX_COLS + 1];
  if (voice && dictation_ == DICT_RECORDING) {
    snprintf(line, sizeof(line), "REC %us  A done  B cancel", (unsigned)((p_.ms() - dictationAt_) / 1000));
    footer(line);
    const uint8_t level = mic_.takeLevel();
    if (level) gridA_.put(gridRows - 1, gridCols - 2, 0x2580 + level, C_OK, C_PANEL);
    return;
  }
  if (voice && dictation_ == DICT_WAITING) {
    footer("Transcribing  B cancel");
    return;
  }
  if (voice && dictationStatus_[0]) {
    if ((int32_t)(p_.ms() - statusUntil_) < 0) {
      footer(dictationStatus_);
      return;
    }
    dictationStatus_[0] = 0;
  }
  if (voice && focused.type == F_TEXT) {
    footer("A talk  AA next  hold B clear");
    return;
  }
  footer(focused.type == F_TEXT ? "Type  Up/Down  Esc back"
         : focused.type == F_CHOICE ? "L/R change  Up/Down  Esc"
         : focused.type == F_TOGGLE ? (voice ? "AA toggle  Up/Down  C back" : "Enter toggle  Up/Down  Esc")
                                    : (voice ? "AA confirm  Up/Down  C back" : "Enter confirm  Up/Down  Esc"));
}

void App::drawForm(const char *title) {
  header(title);
  drawFormFooter();
  int bottom = LIST_BOTTOM;
  if (formError_[0]) {
    bottom--;
    text(LIST_BOTTOM, 1, formError_, C_FAIL, C_BG, gridCols - 2);
  }
  const int rows = bottom - LIST_TOP;
  keepVisible(focus_, formTop_, rows);
  const int labelW = 10;
  for (int i = formTop_; i < fieldCount_ && i < formTop_ + rows; i++) {
    const Field &f = fields_[i];
    const int row = LIST_TOP + i - formTop_;
    const bool on = i == focus_;
    const uint16_t bg = on ? C_SELECT : C_BG;
    for (int c = 0; c < gridCols; c++) gridA_.put(row, c, ' ', C_TEXT, bg);
    if (f.type == F_NOTE) {
      text(row, 1, f.label, C_WARN, bg, gridCols - 2);
    } else if (f.type == F_BUTTON) {
      text(row, labelW + 1, f.label, C_ACCENT, bg, gridCols - labelW - 2);
    } else {
      text(row, 1, f.label, on ? C_TEXT : C_MUTED, bg, labelW - 1);
      const int x = labelW + 1, w = gridCols - x - 1;
      if (f.type == F_TEXT) {
        const bool secret = f.id == FIELD_PASS;
        const int len = strlen(f.text);
        const int cur = on ? textCursor_ : len;
        const int start = cur >= w ? cur - w + 1 : 0;
        for (int c = 0; c < w; c++) {
          const int at = start + c;
          const char ch = at < len ? (secret ? '*' : f.text[at]) : ' ';
          const bool isCursor = on && at == cur;
          gridA_.put(row, x + c, (uint8_t)ch, isCursor ? bg : C_TEXT, isCursor ? C_ACCENT : C_FIELD);
        }
      } else if (f.type == F_TOGGLE) {
        const int after = text(row, x, f.on ? "[x]" : "[ ]", f.on ? C_OK : C_MUTED, bg, 3);
        if (f.value) text(row, after + 1, f.value, f.disabled ? C_MUTED : C_TEXT, bg, gridCols - after - 2);
      } else {
        char value[48];
        snprintf(value, sizeof(value), f.disabled ? "%s" : "< %s >", f.value);
        text(row, x, value, f.disabled ? C_MUTED : C_TEXT, bg, w);
      }
    }
  }
}

void App::drawConfirm() {
  header("Check the certificate");
  footer("Enter it matches  Esc cancel");
  int row = paragraph(1, "Does the Mac's terminal show this?", C_TEXT);
  const int left = (gridCols - 23) / 2;
  char hex[3];
  for (int i = 0; i < 16; i++) {
    snprintf(hex, sizeof(hex), "%02X", pendingFp_[i]);
    const int col = left + (i % 8) * 3;
    text(row + i / 8, col, hex, C_ACCENT, C_BG, 2);
    if (i != 15) text(row + i / 8, col + 2, ":", C_MUTED, C_BG, 1);
  }
  row = paragraph(row + 2, "It's on the line starting \"Certificate\".", C_MUTED);
  paragraph(row, "If not, someone may be intercepting this connection. Don't continue.", C_WARN);
}

static int wrapped(const char *s, int maxCols, int &offset) {
  const int len = strlen(s + offset);
  if (len <= maxCols) return len;
  int cut = maxCols;
  while (cut > 0 && s[offset + cut] != ' ') cut--;
  return cut > 0 ? cut : maxCols;
}

// Draws wrapped text and returns the row after it.
int App::paragraph(int row, const char *s, uint16_t color) {
  int offset = 0;
  const int total = strlen(s);
  char line[MAX_COLS + 1];
  while (offset < total && row < gridRows - 1) {
    const int n = wrapped(s, gridCols - 4, offset);
    memcpy(line, s + offset, n);
    line[n] = 0;
    text(row++, 2, line, color, C_BG, gridCols - 4);
    offset += n;
    while (s[offset] == ' ') offset++;
  }
  return row;
}

void App::drawClosed() {
  header(termName_);
  footer(canReattach_ ? "Enter attach again  Esc back" : "Enter back");
  paragraph(gridRows / 3, closedText_, C_TEXT);
}

void App::drawSettings() {
  header("Settings");
  footer(sel_ == 0 ? "L/R change size  C back" : "Enter choose  C back");
  char size[32];
  snprintf(size, sizeof(size), "Text size < %s %dx%d >", fontName(config_.fontSize), gridCols, termRows);
  const char *items[5] = {size, "Change Wi-Fi", "Pair with a Mac", "Forget keyboards", "Restart"};
  for (int i = 0; i < 5; i++) {
    const int row = LIST_TOP + i;
    const bool on = i == sel_;
    const uint16_t bg = on ? C_SELECT : C_BG;
    for (int c = 0; c < gridCols; c++) gridA_.put(row, c, ' ', C_TEXT, bg);
    text(row, 2, items[i], C_TEXT, bg, gridCols - 4);
  }
  char line[MAX_COLS + 1];
  snprintf(line, sizeof(line), "Wi-Fi  %s", config_.ssid);
  text(LIST_TOP + 6, 2, line, C_MUTED, C_BG, gridCols - 4);
  snprintf(line, sizeof(line), "Mac    %s:%u", config_.host, (unsigned)config_.port);
  text(LIST_TOP + 7, 2, line, C_MUTED, C_BG, gridCols - 4);
}

void App::drawNotice() {
  header("sbx-projects");
  footer("Enter continue");
  paragraph(gridRows / 3, noticeText_, C_TEXT);
}
