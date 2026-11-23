#include "app.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "keymap.h"

// The apps' dark theme.
static const uint16_t C_BG = VT_DEFAULT_BG;
static const uint16_t C_PANEL = rgb565(0x10, 0x1E, 0x2B);
static const uint16_t C_SELECT = rgb565(0x1A, 0x4A, 0x45);
static const uint16_t C_TEXT = VT_DEFAULT_FG;
static const uint16_t C_MUTED = rgb565(0x8A, 0xA2, 0xB2);
static const uint16_t C_ACCENT = VT_CARET;
static const uint16_t C_OK = rgb565(0x6F, 0xDC, 0x9A);
static const uint16_t C_WARN = rgb565(0xF5, 0xB9, 0x4A);
static const uint16_t C_FAIL = rgb565(0xFF, 0x7B, 0x6E);

static const int LIST_TOP = 1;
static const int LIST_BOTTOM = GRID_ROWS - 2;  // the last row holds the key hints
static const uint32_t FLUSH_MS = 40;
static const uint32_t BLINK_MS = 500;
static const int PAGE_NOTCHES = 5;

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
  startNetwork();
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

void App::openNewForm(int projectIndex) {
  newProject_ = projectIndex < 0 ? 0 : projectIndex;
  task_[0] = value_[0] = newKit_[0] = 0;
  sourceIndex_ = 0;
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
  if (!pr) add(Field{F_TOGGLE, FIELD_CLONE, 0, "Private clone", nullptr, 0, clone_, nullptr, false});
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
    add(Field{F_TOGGLE, FIELD_MCP, (uint8_t)i, m.ready ? "MCP" : "MCP (setup)", nullptr, 0, m.picked, m.name, !m.ready && !m.picked});
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
  for (int i = 0; i < options_.kitCount; i++) {
    if (options_.kitPicked[i]) r.kits[r.kitCount++] = options_.kits[i];
  }
  for (int i = 0; i < options_.mcpCount; i++) {
    if (options_.mcps[i].picked) r.mcps[r.mcpCount++] = options_.mcps[i].name;
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
  snprintf(suffix, sizeof(suffix), "/terminal?cols=%d&rows=%d%s", GRID_COLS, TERM_ROWS, shell ? "&shell=1" : "");
  sandboxPath(path, sizeof(path), name, suffix);
  if (!ws_.open(config_, path)) {
    snprintf(closedText_, sizeof(closedText_), "%s", ws_.error());
    canReattach_ = ws_.closeCode() != 4001;
    go(V_CLOSED);
    return;
  }
  vt_.reset();
  scrollDepth_ = 0;
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
    if (n == 0) break;
    vt_.feed(termBuf_, n);
  }
}

void App::terminalClosed() {
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
  const int col = GRID_COLS / 2, row = TERM_ROWS / 2;
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

void App::handleTerminal(const Input &in) {
  switch (in.key) {
    case BTN_C:
      detach();
      return;
    case BTN_B:
      if (p_.keyboard() == KB_LOST) p_.restart();
      return;
    case BTN_A:
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
      if (scrollDepth_ > 0) wheel(false, scrollDepth_ + 1);
      scrollDepth_ = 0;
      return;
    default:
      break;
  }
  char bytes[16];
  const size_t n = keyToBytes(in, vt_.appCursor(), bytes);
  if (n && !ws_.sendBinary((const uint8_t *)bytes, n)) terminalClosed();
}

void App::drawTerminalBar() {
  Grid *g = vt_.grid();
  char left[GRID_COLS + 1];
  snprintf(left, sizeof(left), " %s%s", termName_, termShell_ ? " (shell)" : "");
  const KeyboardState kb = p_.keyboard();
  const char *right = kb == KB_LOST ? "KB LOST: B restarts " : scrollDepth_ > 0 ? "SCROLL: press=live " : "C detach ";
  const int rightLen = strlen(right);
  const uint16_t bg = kb == KB_LOST ? 0x8000 : C_PANEL;
  const uint16_t fg = scrollDepth_ > 0 ? C_WARN : C_MUTED;
  for (int c = 0; c < GRID_COLS; c++) g->put(0, c, ' ', C_TEXT, bg);
  const int leftMax = GRID_COLS - rightLen - 1;
  for (int c = 0; c < leftMax && left[c]; c++) g->put(0, c, (uint8_t)left[c], C_ACCENT, bg);
  for (int c = 0; c < rightLen; c++) g->put(0, GRID_COLS - rightLen + c, (uint8_t)right[c], kb == KB_LOST ? C_TEXT : fg, bg);
}

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

void App::tick() {
  Input in;
  int handled = 0;
  while (handled++ < 8 && p_.poll(in)) handle(in);

  if (view_ == V_TERMINAL && termOpen_) pumpTerminal();

  if (view_ == V_TERMINAL) {
    const uint32_t now = p_.ms();
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
    case V_DETAIL:
      if (in.key == BTN_A || (in.key == KEY_TEXT && in.ch == 'r')) {
        loadSandboxes();
        go(V_DETAIL);
        return;
      }
      handleList(in, 3);
      break;
    case V_SETTINGS:
      handleList(in, 4);
      break;
    case V_WIFI_PASS:
    case V_SERVER:
    case V_NEW:
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
      else sandboxAction(s->running ? "stop" : "resume");
      break;
    }
    case V_SETTINGS:
      if (sel_ == 0) {
        scanWifi();
      } else if (sel_ == 1) {
        startServerForm();
      } else if (sel_ == 2) {
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
  for (int r = 0; r < GRID_ROWS; r++) {
    for (int c = 0; c < GRID_COLS; c++) gridA_.put(r, c, ' ', C_TEXT, C_BG);
  }
}

// Draws UTF-8 text and returns the column after it.
int App::text(int row, int col, const char *s, uint16_t fg, uint16_t bg, int maxCols) {
  const uint8_t *u = (const uint8_t *)s;
  int used = 0;
  while (*u && used < maxCols && col + used < GRID_COLS) {
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
  for (int c = 0; c < GRID_COLS; c++) gridA_.put(0, c, ' ', C_TEXT, C_PANEL);
  const KeyboardState kb = p_.keyboard();
  const char *status = kb == KB_CONNECTED ? "KB" : kb == KB_LOST ? "KB LOST" : "no KB";
  const int len = strlen(status);
  text(0, 1, title, C_ACCENT, C_PANEL, GRID_COLS - len - 3);
  text(0, GRID_COLS - len - 1, status, kb == KB_CONNECTED ? C_OK : C_WARN, C_PANEL, len);
}

void App::footer(const char *hints) {
  for (int c = 0; c < GRID_COLS; c++) gridA_.put(GRID_ROWS - 1, c, ' ', C_MUTED, C_PANEL);
  text(GRID_ROWS - 1, 1, hints, C_MUTED, C_PANEL, GRID_COLS - 2);
}

void App::busy(const char *message) {
  if (view_ == V_TERMINAL) view_ = V_NOTICE;
  clearUi();
  header("sbx-projects");
  text(GRID_ROWS / 2, (GRID_COLS - (int)strlen(message)) / 2, message, C_TEXT, C_BG, GRID_COLS);
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
    renderRow(g->cells[r], r == cursorRow && blinkOn_ ? cursorCol : -1, strip_);
    p_.pushStrip(r * STRIP_H, strip_);
  }
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
    text(3, 2, "No networks found. Press A to look again.", C_MUTED, C_BG, GRID_COLS - 4);
    return;
  }
  const int rows = LIST_BOTTOM - LIST_TOP;
  keepVisible(sel_, top_, rows);
  for (int i = top_; i < networkCount_ && i < top_ + rows; i++) {
    const int row = LIST_TOP + 1 + i - top_;
    const bool on = i == sel_;
    const uint16_t bg = on ? C_SELECT : C_BG;
    for (int c = 0; c < GRID_COLS; c++) gridA_.put(row, c, ' ', C_TEXT, bg);
    text(row, 1, networks_[i].ssid, C_TEXT, bg, 36);
    char right[16];
    snprintf(right, sizeof(right), "%s%4d", networks_[i].open ? "open" : "", networks_[i].rssi);
    text(row, GRID_COLS - 1 - strlen(right), right, C_MUTED, bg, 12);
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
    for (int c = 0; c < GRID_COLS; c++) gridA_.put(row, c, ' ', C_TEXT, bg);
    if (i == 0) {
      text(row, 1, "All sandboxes", C_ACCENT, bg, 40);
      char n[8];
      snprintf(n, sizeof(n), "%d", sandboxCount_);
      text(row, GRID_COLS - 1 - strlen(n), n, C_MUTED, bg, 6);
    } else if (i <= projectCount_) {
      const ProjectInfo &pr = projects_[i - 1];
      const int after = text(row, 1, pr.name, C_TEXT, bg, 30);
      text(row, after + 1, pr.branch, C_MUTED, bg, GRID_COLS - after - 6);
      char n[8];
      snprintf(n, sizeof(n), "%d", pr.sandboxes);
      text(row, GRID_COLS - 1 - strlen(n), n, C_MUTED, bg, 6);
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
  footer("Enter open  C back  B new  A refresh");
  const int count = visibleSandboxCount();
  if (count == 0) {
    text(3, 2, "No sandboxes yet. Press B to make one.", C_MUTED, C_BG, GRID_COLS - 4);
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
    for (int c = 0; c < GRID_COLS; c++) {
      gridA_.put(row, c, ' ', C_TEXT, bg);
      gridA_.put(row + 1, c, ' ', C_TEXT, bg);
    }
    gridA_.put(row, 1, s.running ? 0x25CF : 0x25CB, stateColor(s), bg);
    text(row, 3, s.title, C_TEXT, bg, GRID_COLS - 7);
    if (s.unread) gridA_.put(row, GRID_COLS - 2, 0x2022, C_ACCENT, bg);
    const int after = text(row + 1, 3, s.status, stateColor(s), bg, 24);
    text(row + 1, after + 2, s.branch, C_MUTED, bg, GRID_COLS - after - 3);
  }
}

void App::drawDetail() {
  SandboxInfo *s = selectedSandbox();
  if (!s) {
    header(detailName_);
    footer("C back");
    text(3, 2, "Starting. Press A to refresh.", C_MUTED, C_BG, GRID_COLS - 4);
    return;
  }
  header(s->name);
  footer("Enter choose  C back  A refresh");
  int row = 2;
  text(row++, 1, s->title, C_TEXT, C_BG, GRID_COLS - 2);
  row++;
  const auto line = [&](const char *label, const char *value, uint16_t color) {
    text(row, 1, label, C_MUTED, C_BG, 8);
    text(row, 9, value, color, C_BG, GRID_COLS - 10);
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
  row++;

  const char *actions[3] = {"Attach to agent", "Open a shell", s->running ? "Stop sandbox" : "Resume sandbox"};
  for (int i = 0; i < 3; i++, row++) {
    const bool on = i == sel_;
    const uint16_t bg = on ? C_SELECT : C_BG;
    for (int c = 0; c < GRID_COLS; c++) gridA_.put(row, c, ' ', C_TEXT, bg);
    text(row, 2, actions[i], i == 0 ? C_ACCENT : C_TEXT, bg, GRID_COLS - 4);
  }
}

void App::drawForm(const char *title) {
  header(title);
  const Field &focused = fields_[focus_];
  footer(focused.type == F_TEXT ? "Type  Up/Down field  Esc back"
         : focused.type == F_CHOICE ? "Left/Right change  Up/Down field  Esc back"
         : focused.type == F_TOGGLE ? "Enter toggle  Up/Down field  Esc back"
                                    : "Enter confirm  Up/Down field  Esc back");
  int bottom = LIST_BOTTOM;
  if (formError_[0]) {
    bottom--;
    text(LIST_BOTTOM, 1, formError_, C_FAIL, C_BG, GRID_COLS - 2);
  }
  const int rows = bottom - LIST_TOP;
  keepVisible(focus_, formTop_, rows);
  const int labelW = 14;
  for (int i = formTop_; i < fieldCount_ && i < formTop_ + rows; i++) {
    const Field &f = fields_[i];
    const int row = LIST_TOP + i - formTop_;
    const bool on = i == focus_;
    const uint16_t bg = on ? C_SELECT : C_BG;
    for (int c = 0; c < GRID_COLS; c++) gridA_.put(row, c, ' ', C_TEXT, bg);
    if (f.type == F_NOTE) {
      text(row, 1, f.label, C_WARN, bg, GRID_COLS - 2);
    } else if (f.type == F_BUTTON) {
      text(row, labelW + 1, f.label, C_ACCENT, bg, GRID_COLS - labelW - 2);
    } else {
      text(row, 1, f.label, on ? C_TEXT : C_MUTED, bg, labelW - 1);
      const int x = labelW + 1, w = GRID_COLS - x - 1;
      if (f.type == F_TEXT) {
        const bool secret = f.id == FIELD_PASS;
        const int len = strlen(f.text);
        const int cur = on ? textCursor_ : len;
        const int start = cur >= w ? cur - w + 1 : 0;
        for (int c = 0; c < w; c++) {
          const int at = start + c;
          const char ch = at < len ? (secret ? '*' : f.text[at]) : ' ';
          const bool isCursor = on && at == cur;
          gridA_.put(row, x + c, (uint8_t)ch, isCursor ? bg : C_TEXT, isCursor ? C_ACCENT : C_PANEL);
        }
      } else if (f.type == F_TOGGLE) {
        const int after = text(row, x, f.on ? "[x]" : "[ ]", f.on ? C_OK : C_MUTED, bg, 3);
        if (f.value) text(row, after + 1, f.value, f.disabled ? C_MUTED : C_TEXT, bg, GRID_COLS - after - 2);
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
  text(3, 2, "Does the Mac's terminal show this?", C_TEXT, C_BG, GRID_COLS - 4);
  char hex[3];
  for (int i = 0; i < 16; i++) {
    snprintf(hex, sizeof(hex), "%02X", pendingFp_[i]);
    const int row = 6 + i / 8 * 2;
    const int col = 3 + (i % 8) * 6;
    text(row, col, hex, C_ACCENT, C_BG, 2);
    if (i % 8 != 7) text(row, col + 3, ":", C_MUTED, C_BG, 1);
  }
  text(12, 2, "It's on the line starting \"Certificate\".", C_MUTED, C_BG, GRID_COLS - 4);
  text(14, 2, "If it differs, someone may be intercepting", C_WARN, C_BG, GRID_COLS - 4);
  text(15, 2, "this connection. Don't continue.", C_WARN, C_BG, GRID_COLS - 4);
}

static int wrapped(const char *s, int maxCols, int &offset) {
  const int len = strlen(s + offset);
  if (len <= maxCols) return len;
  int cut = maxCols;
  while (cut > 0 && s[offset + cut] != ' ') cut--;
  return cut > 0 ? cut : maxCols;
}

void App::drawClosed() {
  header(termName_);
  footer(canReattach_ ? "Enter attach again  Esc back" : "Enter back");
  int offset = 0, row = 10;
  const int total = strlen(closedText_);
  char line[GRID_COLS + 1];
  while (offset < total && row < 20) {
    const int n = wrapped(closedText_, GRID_COLS - 4, offset);
    memcpy(line, closedText_ + offset, n);
    line[n] = 0;
    text(row++, 2, line, C_TEXT, C_BG, GRID_COLS - 4);
    offset += n;
    while (closedText_[offset] == ' ') offset++;
  }
}

void App::drawSettings() {
  header("Settings");
  footer("Enter choose  C back");
  const char *items[4] = {"Change Wi-Fi", "Pair with a Mac", "Forget keyboards and restart", "Restart"};
  for (int i = 0; i < 4; i++) {
    const int row = LIST_TOP + 1 + i;
    const bool on = i == sel_;
    const uint16_t bg = on ? C_SELECT : C_BG;
    for (int c = 0; c < GRID_COLS; c++) gridA_.put(row, c, ' ', C_TEXT, bg);
    text(row, 2, items[i], C_TEXT, bg, GRID_COLS - 4);
  }
  char line[GRID_COLS + 1];
  snprintf(line, sizeof(line), "Wi-Fi  %s", config_.ssid);
  text(8, 2, line, C_MUTED, C_BG, GRID_COLS - 4);
  snprintf(line, sizeof(line), "Mac    %s:%u", config_.host, (unsigned)config_.port);
  text(9, 2, line, C_MUTED, C_BG, GRID_COLS - 4);
}

void App::drawNotice() {
  header("sbx-projects");
  footer("Enter continue");
  int offset = 0, row = 10;
  const int total = strlen(noticeText_);
  char line[GRID_COLS + 1];
  while (offset < total && row < 20) {
    const int n = wrapped(noticeText_, GRID_COLS - 4, offset);
    memcpy(line, noticeText_ + offset, n);
    line[n] = 0;
    text(row++, 2, line, C_TEXT, C_BG, GRID_COLS - 4);
    offset += n;
    while (noticeText_[offset] == ' ') offset++;
  }
}
