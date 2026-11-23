#include "api.h"

#include <stdio.h>
#include <string.h>

#include <ArduinoJson.h>

template <size_t N>
static void copyStr(char (&dst)[N], const char *src) {
  strncpy(dst, src ? src : "", N - 1);
  dst[N - 1] = 0;
  // Don't leave half a UTF-8 character at the end.
  size_t n = strlen(dst);
  if (n == N - 1) {
    while (n > 0 && ((uint8_t)dst[n - 1] & 0xC0) == 0x80) n--;
    if (n > 0 && ((uint8_t)dst[n - 1] & 0xC0) == 0xC0) dst[n - 1] = 0;
  }
}

void sandboxPath(char *out, size_t cap, const char *name, const char *suffix) {
  size_t n = (size_t)snprintf(out, cap, "/v1/sandboxes/");
  for (const char *s = name; *s && n + 4 < cap; s++) {
    const char c = *s;
    if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' ||
        c == '_' || c == '.') {
      out[n++] = c;
    } else {
      n += snprintf(out + n, cap - n, "%%%02X", (uint8_t)c);
    }
  }
  out[n] = 0;
  snprintf(out + n, cap - n, "%s", suffix);
}

void Api::readError(int status) {
  unauthorized_ = status == 401;
  if (status == 0) {
    snprintf(error_, sizeof(error_), "%s", http_.error());
    return;
  }
  JsonDocument doc;
  if (deserializeJson(doc, http_) == DeserializationError::Ok && doc["error"].is<const char *>()) {
    snprintf(error_, sizeof(error_), "%s", doc["error"].as<const char *>());
  } else {
    snprintf(error_, sizeof(error_), "The server answered %d", status);
  }
}

bool Api::begin(const char *method, const char *path, const char *body, int okStatus,
                bool withToken) {
  error_[0] = 0;
  unauthorized_ = false;
  const int status = http_.request(config_, method, path, body, withToken);
  if (status == okStatus) return true;
  readError(status);
  http_.end();
  return false;
}

bool Api::probeCertificate(const char *host, uint16_t port, uint8_t *fingerprint) {
  error_[0] = 0;
  if (!tls_.connect(host, port, nullptr)) {
    snprintf(error_, sizeof(error_), "%s", tls_.error());
    return false;
  }
  memcpy(fingerprint, tls_.seenFingerprint(), 32);
  tls_.close();
  return true;
}

bool Api::pair(const char *code) {
  JsonDocument req;
  req["code"] = code;
  req["device"] = "Wio Terminal";
  char body[128];
  serializeJson(req, body, sizeof(body));
  if (!begin("POST", "/v1/pair", body, 200, false)) return false;

  JsonDocument filter;
  filter["token"] = true;
  JsonDocument doc;
  const DeserializationError e = deserializeJson(doc, http_, DeserializationOption::Filter(filter));
  http_.end();
  const char *token = doc["token"] | "";
  if (e || strlen(token) != 64) {
    snprintf(error_, sizeof(error_), "The server's answer wasn't understood");
    return false;
  }
  copyStr(config_.token, token);
  config_.paired = true;
  return true;
}

int Api::projects(ProjectInfo *out, int max) {
  if (!begin("GET", "/v1/projects", nullptr, 200)) return -1;
  JsonDocument filter;
  filter[0]["id"] = true;
  filter[0]["name"] = true;
  filter[0]["defaultBranch"] = true;
  JsonDocument doc;
  const DeserializationError e = deserializeJson(doc, http_, DeserializationOption::Filter(filter));
  http_.end();
  if (e) {
    snprintf(error_, sizeof(error_), "Couldn't read the projects: %s", e.c_str());
    return -1;
  }
  int n = 0;
  for (JsonObject o : doc.as<JsonArray>()) {
    if (n == max) break;
    ProjectInfo &p = out[n++];
    p.id = o["id"] | (int64_t)0;
    copyStr(p.name, o["name"] | "");
    copyStr(p.branch, o["defaultBranch"] | "");
    p.sandboxes = 0;
  }
  return n;
}

int Api::sandboxes(SandboxInfo *out, int max) {
  if (!begin("GET", "/v1/sandboxes", nullptr, 200)) return -1;
  JsonDocument filter;
  for (const char *key : {"name", "projectId", "title", "branch", "agentLabel", "modelLabel", "state",
                          "statusText", "running", "agentUp", "busy", "unread", "added", "removed",
                          "files"}) {
    filter[0][key] = true;
  }
  JsonDocument doc;
  const DeserializationError e = deserializeJson(doc, http_, DeserializationOption::Filter(filter));
  http_.end();
  if (e) {
    snprintf(error_, sizeof(error_), "Couldn't read the sandboxes: %s", e.c_str());
    return -1;
  }
  int n = 0;
  for (JsonObject o : doc.as<JsonArray>()) {
    if (n == max) break;
    SandboxInfo &s = out[n++];
    s.projectId = o["projectId"] | (int64_t)0;
    copyStr(s.name, o["name"] | "");
    copyStr(s.title, o["title"] | "");
    copyStr(s.branch, o["branch"] | "");
    copyStr(s.agent, o["agentLabel"] | "");
    copyStr(s.model, o["modelLabel"] | "");
    copyStr(s.state, o["state"] | "");
    copyStr(s.status, o["statusText"] | "");
    s.running = o["running"] | false;
    s.agentUp = o["agentUp"] | false;
    s.busy = o["busy"] | false;
    s.unread = o["unread"] | false;
    s.added = o["added"] | 0;
    s.removed = o["removed"] | 0;
    s.files = o["files"] | 0;
  }
  return n;
}

bool Api::options(int64_t project, SandboxOptions &out) {
  char path[48];
  snprintf(path, sizeof(path), "/v1/options?project=%ld", (long)project);
  if (!begin("GET", path, nullptr, 200)) return false;
  JsonDocument doc;
  const DeserializationError e = deserializeJson(doc, http_);
  http_.end();
  if (e) {
    snprintf(error_, sizeof(error_), "Couldn't read the options: %s", e.c_str());
    return false;
  }

  memset(&out, 0, sizeof(out));
  JsonObject defaults = doc["defaults"];
  const char *defaultAgent = defaults["agent"] | "claude";
  out.defaultClone = defaults["clone"] | true;
  for (JsonObject a : doc["agents"].as<JsonArray>()) {
    if (out.agentCount == MAX_AGENTS) break;
    AgentOption &agent = out.agents[out.agentCount];
    copyStr(agent.id, a["id"] | "");
    copyStr(agent.label, a["label"] | "");
    const char *remembered = defaults["models"][agent.id] | "";
    for (JsonObject m : a["models"].as<JsonArray>()) {
      if (agent.modelCount == MAX_MODELS) break;
      ModelOption &model = agent.models[agent.modelCount];
      copyStr(model.id, m["id"] | "");
      copyStr(model.label, m["label"] | "");
      if (strcmp(model.id, remembered) == 0) agent.defaultModel = agent.modelCount;
      agent.modelCount++;
    }
    if (strcmp(agent.id, defaultAgent) == 0) out.defaultAgent = out.agentCount;
    out.agentCount++;
  }
  for (JsonVariant k : defaults["kits"].as<JsonArray>()) {
    if (out.kitCount == MAX_KITS) break;
    copyStr(out.kits[out.kitCount], k.as<const char *>());
    out.kitPicked[out.kitCount++] = true;
  }
  if (doc["mcpServers"].is<JsonArray>()) {
    for (JsonObject m : doc["mcpServers"].as<JsonArray>()) {
      if (out.mcpCount == MAX_MCPS) break;
      McpOption &mcp = out.mcps[out.mcpCount++];
      copyStr(mcp.name, m["name"] | "");
      mcp.ready = m["ready"] | false;
      for (JsonVariant picked : defaults["mcps"].as<JsonArray>()) {
        if (strcmp(picked.as<const char *>(), mcp.name) == 0) mcp.picked = true;
      }
    }
  } else {
    copyStr(out.mcpError, doc["mcpError"] | "Couldn't list the MCP servers");
  }
  return true;
}

bool Api::create(const NewSandbox &r, char *createdName, size_t cap) {
  JsonDocument req;
  req["projectId"] = r.projectId;
  req["agent"] = r.agent;
  req["source"] = r.source;
  req["value"] = r.value;
  req["task"] = r.task;
  req["clone"] = r.clone;
  req["model"] = r.model;
  JsonArray kits = req["kits"].to<JsonArray>();
  for (int i = 0; i < r.kitCount; i++) kits.add(r.kits[i]);
  JsonArray mcps = req["mcps"].to<JsonArray>();
  for (int i = 0; i < r.mcpCount; i++) mcps.add(r.mcps[i]);

  static char body[3072];
  if (serializeJson(req, body, sizeof(body)) >= sizeof(body) - 1) {
    snprintf(error_, sizeof(error_), "The form is too long to send");
    return false;
  }
  if (!begin("POST", "/v1/sandboxes", body, 202)) return false;
  JsonDocument filter;
  filter["name"] = true;
  JsonDocument doc;
  deserializeJson(doc, http_, DeserializationOption::Filter(filter));
  http_.end();
  snprintf(createdName, cap, "%s", doc["name"] | "");
  return true;
}

bool Api::action(const char *name, const char *verb) {
  char path[128], suffix[16];
  snprintf(suffix, sizeof(suffix), "/%s", verb);
  sandboxPath(path, sizeof(path), name, suffix);
  if (!begin("POST", path, nullptr, 202)) return false;
  http_.end();
  return true;
}
