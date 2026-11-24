// The parts of the sbx-projects API the Wio uses, in fixed-size structs.
#pragma once

#include "net.h"

static const int MAX_PROJECTS = 24;
static const int MAX_SANDBOXES = 32;
static const int MAX_AGENTS = 4;
static const int MAX_MODELS = 8;
static const int MAX_KITS = 8;
static const int MAX_MCPS = 12;

struct ProjectInfo {
  int64_t id;
  char name[32];
  char branch[24];
  uint8_t sandboxes;
};

struct SandboxInfo {
  int64_t projectId;
  char name[64];
  bool nameTooLong;  // cut short, so it could name another sandbox
  char title[64];
  char branch[40];
  char agent[16];
  char model[24];
  char state[10];
  char status[44];
  bool running, agentUp, busy, unread;
  uint16_t added, removed, files;
};

struct ModelOption {
  char id[32];
  char label[24];
};

struct AgentOption {
  char id[12];
  char label[16];
  ModelOption models[MAX_MODELS];
  uint8_t modelCount;
  uint8_t defaultModel;
};

struct McpOption {
  char name[32];
  bool ready;
  bool picked;
  bool tooLong;  // name is cut short, so sbx wouldn't recognize it
};

struct SandboxOptions {
  AgentOption agents[MAX_AGENTS];
  uint8_t agentCount;
  uint8_t defaultAgent;
  bool defaultClone;
  char kits[MAX_KITS][192];  // references can be long git URLs
  bool kitPicked[MAX_KITS];
  bool kitTooLong[MAX_KITS];  // cut short, so sbx wouldn't find it
  uint8_t kitCount;
  McpOption mcps[MAX_MCPS];
  uint8_t mcpCount;
  char mcpError[64];
};

struct NewSandbox {
  int64_t projectId;
  const char *agent;
  const char *source;
  const char *value;
  const char *task;
  bool clone;
  const char *model;
  const char *kits[MAX_KITS];
  uint8_t kitCount;
  const char *mcps[MAX_MCPS];
  uint8_t mcpCount;
};

class Api {
 public:
  Api(Platform &platform, Config &config, PinnedTls &tls)
      : config_(config), tls_(tls), http_(platform, tls) {}

  // Connects without a pin to read the server's certificate. Nothing is sent.
  bool probeCertificate(const char *host, uint16_t port, uint8_t *fingerprint);
  // Trades a pairing code for a token, over a connection pinned to config.fingerprint.
  bool pair(const char *code);

  int projects(ProjectInfo *out, int max);
  int sandboxes(SandboxInfo *out, int max);
  bool options(int64_t project, SandboxOptions &out);
  bool create(const NewSandbox &req, char *createdName, size_t cap);
  bool action(const char *name, const char *verb);
  // Removes the sandbox and its record, stopping it first if it's running.
  bool remove(const char *name);

  const char *error() const { return error_; }
  bool unauthorized() const { return unauthorized_; }

 private:
  bool begin(const char *method, const char *path, const char *body, int okStatus,
             bool withToken = true);
  void readError(int status);

  Config &config_;
  PinnedTls &tls_;
  Http http_;
  char error_[120] = "";
  bool unauthorized_ = false;
};

// Writes the URL path of a sandbox's endpoint, percent-encoding its name. Returns false, leaving
// out empty, if it doesn't fit, because a cut name could be another sandbox's.
bool sandboxPath(char *out, size_t cap, const char *name, const char *suffix);
