#include "ble_keyboard.h"

#include <rpcBLEDevice.h>
#include <BLEScan.h>
#include <BLEAdvertisedDevice.h>

#include "debug_log.h"
#include "src/ble_adv.h"
#include "src/keymap.h"

// rpcBLE keeps its GATT client / GAP message handlers in these globals (rpc_ble_callback_impl.cpp).
// We wrap them so we can see notifications, write results and pairing events directly.
extern P_FUN_GENERAL_APP_CB _ble_gattc_callback;
extern P_FUN_HABDLE_GAP_MSG _handle_gap_msg;
extern P_FUN_LE_APP_CB _ble_gap_callback;

namespace BleKeyboard {
namespace {

P_FUN_GENERAL_APP_CB libGattcCallback = nullptr;
P_FUN_HABDLE_GAP_MSG libGapMsgHandler = nullptr;
P_FUN_LE_APP_CB libGapAppCallback = nullptr;

// Where a connection attempt has got to, kept in backup RAM so it survives the watchdog's reset
// and the next boot can say where it hung.
enum Step : uint8_t {
  STEP_SCAN, STEP_CREATE, STEP_LIB_CONNECT, STEP_WAIT_LINK, STEP_BOND, STEP_WAIT_AUTH, STEP_SERVICES,
  STEP_CHARS, STEP_PROTOCOL, STEP_SUBSCRIBE, STEP_LEDS,
};
const char *const STEP_NAMES[] = {"scan", "create", "connect", "link", "bond", "auth", "services",
                                  "chars", "protocol", "subscribe", "leds"};
enum EventKind : uint8_t { EVENT_NONE, EVENT_GAP_MSG, EVENT_GAP_APP, EVENT_GATT };
const char *const EVENT_NAMES[] = {"none", "msg", "app", "gatt"};
const uint32_t CRUMB_MAGIC = 0x53425842;

struct Crumb {
  uint32_t magic;
  uint8_t step, link, handlerKind, handlerType, lastKind, lastType;
};
volatile Crumb *const crumb = (volatile Crumb *)BKUPRAM_ADDR;

void setStep(Step step) {
  crumb->step = step;
  logf("ble: step %s, free heap %u", STEP_NAMES[step], (unsigned)freeHeap());
}

void noteEvent(EventKind kind, uint8_t type) {
  crumb->lastKind = kind;
  crumb->lastType = type;
}
void enterHandler(EventKind kind, uint8_t type) {
  noteEvent(kind, type);
  crumb->handlerType = type;
  crumb->handlerKind = kind;
}
void leaveHandler() { crumb->handlerKind = EVENT_NONE; }

const uint16_t UUID_HID_SERVICE = 0x1812;
const uint16_t UUID_REPORT = 0x2A4D;
const uint16_t UUID_PROTOCOL_MODE = 0x2A4E;
const uint16_t UUID_BOOT_KEYBOARD_INPUT = 0x2A22;
const uint16_t UUID_BOOT_KEYBOARD_OUTPUT = 0x2A32;
const uint16_t UUID_CCCD = 0x2902;
const uint8_t PROTOCOL_MODE_BOOT = 0x00;
const uint16_t CHAR_PROP_WRITE = 0x08;
const uint16_t CHAR_PROP_NOTIFY = 0x10;
const uint8_t HID_LED_CAPS_LOCK = 0x02;
const uint32_t REPEAT_DELAY_MS = 500;
const uint32_t REPEAT_RATE_MS = 50;

// State shared with BLE callbacks. Callbacks run on the eRPC server thread, which also receives
// RPC replies, so they must never make an RPC call themselves: it would never complete. They
// only record what happened; the main loop acts on it.
struct Report {
  uint16_t handle;
  uint8_t len;
  uint8_t data[16];
};

const uint8_t QUEUE_SIZE = 32;
Report reportQueue[QUEUE_SIZE];
volatile uint8_t queueHead = 0;
volatile uint8_t queueTail = 0;

const uint8_t MAX_INPUT_HANDLES = 6;
uint16_t inputHandles[MAX_INPUT_HANDLES];
volatile uint8_t inputHandleCount = 0;

volatile bool linkUp = false;
volatile bool linkLost = false;
volatile uint8_t linkConnId = 0xff;
volatile bool authDone = false;
volatile uint16_t authStatus = 0;
volatile uint16_t pendingBondMsg = 0;
volatile uint8_t pendingBondConnId = 0;
volatile bool writeDone = false;
volatile uint16_t writeCause = 0;

// rpcBLE's own discovery starts the request before arming its semaphore, so it can return empty
// or hang forever; we run discovery ourselves.
struct GattService {
  uint16_t start, end, uuid;
};
struct GattChar {
  uint16_t declHandle, valueHandle, properties, uuid;
};
struct GattDesc {
  uint16_t handle, uuid;
};
GattService services[16];
GattChar chars[24];
GattDesc descs[16];
volatile uint8_t serviceCount = 0;
volatile uint8_t charCount = 0;
volatile uint8_t descCount = 0;

enum DiscoveryState : int8_t { DISC_PENDING, DISC_DONE, DISC_FAILED };
volatile DiscoveryState discoveryState = DISC_PENDING;

BLEAdvertisedDevice *keyboardDevice = nullptr;
BLEClient *client = nullptr;
bool ready = false;

uint8_t prevKeys[6] = {0};
bool capsLock = false;
Input repeatInput;
uint8_t repeatUsage = 0;
uint32_t repeatAt = 0;

const uint8_t INPUT_QUEUE = 16;
Input inputs[INPUT_QUEUE];
uint8_t inputHead = 0, inputTail = 0;

void pushInput(const Input &in) {
  const uint8_t next = (inputHead + 1) % INPUT_QUEUE;
  if (next == inputTail) return;
  inputs[inputHead] = in;
  inputHead = next;
}

bool isInputHandle(uint16_t handle) {
  for (uint8_t i = 0; i < inputHandleCount; i++) {
    if (inputHandles[i] == handle) return true;
  }
  return false;
}

void enqueueReport(uint16_t handle, const uint8_t *data, uint16_t len) {
  const uint8_t next = (queueHead + 1) % QUEUE_SIZE;
  if (next == queueTail) return;  // full: drop
  Report &r = reportQueue[queueHead];
  r.handle = handle;
  r.len = len > sizeof(r.data) ? sizeof(r.data) : len;
  memcpy(r.data, data, r.len);
  queueHead = next;
}

bool dequeueReport(Report &out) {
  if (queueTail == queueHead) return false;
  out = reportQueue[queueTail];
  queueTail = (queueTail + 1) % QUEUE_SIZE;
  return true;
}

T_APP_RESULT gattcHook(T_CLIENT_ID clientId, uint8_t connId, void *pData) {
  T_BLE_CLIENT_CB_DATA *cb = (T_BLE_CLIENT_CB_DATA *)pData;
  switch (cb->cb_type) {
    case BLE_CLIENT_CB_TYPE_NOTIF_IND: {
      const T_BLE_CLIENT_NOTIF_IND &n = cb->cb_content.notif_ind;
      enqueueReport(n.handle, n.p_value, n.value_size);
      // Not forwarded: the library would block in / call callbacks we never registered.
      return APP_RESULT_SUCCESS;
    }
    case BLE_CLIENT_CB_TYPE_WRITE_RESULT:
      writeCause = cb->cb_content.write_result.cause;
      writeDone = true;
      return APP_RESULT_SUCCESS;
    case BLE_CLIENT_CB_TYPE_DISCOVERY_STATE:
      switch (cb->cb_content.discov_state.state) {
        case DISC_STATE_SRV_DONE:
        case DISC_STATE_CHAR_DONE:
        case DISC_STATE_CHAR_UUID16_DONE:
        case DISC_STATE_CHAR_UUID128_DONE:
        case DISC_STATE_CHAR_DESCRIPTOR_DONE:
          discoveryState = DISC_DONE;
          break;
        case DISC_STATE_FAILED:
          discoveryState = DISC_FAILED;
          break;
        default:
          break;
      }
      return APP_RESULT_SUCCESS;
    case BLE_CLIENT_CB_TYPE_DISCOVERY_RESULT: {
      const T_BLE_CLIENT_DISCOVERY_RESULT &d = cb->cb_content.discov_result;
      switch (d.discov_type) {
        case DISC_RESULT_ALL_SRV_UUID16:
          if (serviceCount < sizeof(services) / sizeof(services[0])) {
            const T_GATT_SERVICE_ELEM16 &s = d.result.srv_uuid16_disc_data;
            services[serviceCount++] = {s.att_handle, s.end_group_handle, s.uuid16};
          }
          break;
        case DISC_RESULT_ALL_SRV_UUID128:
          if (serviceCount < sizeof(services) / sizeof(services[0])) {
            const T_GATT_SERVICE_ELEM128 &s = d.result.srv_uuid128_disc_data;
            services[serviceCount++] = {s.att_handle, s.end_group_handle, 0};
          }
          break;
        case DISC_RESULT_CHAR_UUID16:
          if (charCount < sizeof(chars) / sizeof(chars[0])) {
            const T_GATT_CHARACT_ELEM16 &c = d.result.char_uuid16_disc_data;
            chars[charCount++] = {c.decl_handle, c.value_handle, c.properties, c.uuid16};
          }
          break;
        case DISC_RESULT_CHAR_DESC_UUID16:
          if (descCount < sizeof(descs) / sizeof(descs[0])) {
            const T_GATT_CHARACT_DESC_ELEM16 &e = d.result.char_desc_uuid16_disc_data;
            descs[descCount++] = {e.handle, e.uuid16};
          }
          break;
        default:
          break;
      }
      return APP_RESULT_SUCCESS;
    }
    default: {
      enterHandler(EVENT_GATT, cb->cb_type);
      const T_APP_RESULT result = libGattcCallback(clientId, connId, pData);
      leaveHandler();
      return result;
    }
  }
}

// Logs each named or matching device once, and again if its name turns up in a later packet.
void logDevice(const T_LE_SCAN_INFO &scan, const AdvInfo &info, bool match) {
  if (!match && !info.name[0]) return;
  static uint32_t seen[32];
  static bool named[32];
  static uint8_t count = 0;
  uint32_t hash = 2166136261UL;
  for (int i = 0; i < 6; i++) hash = (hash ^ scan.bd_addr[i]) * 16777619UL;
  int at = -1;
  for (int i = 0; i < count; i++) {
    if (seen[i] == hash) at = i;
  }
  if (at >= 0 && (named[at] || !info.name[0])) return;
  if (at < 0) {
    if (count == 32) return;
    at = count++;
    seen[at] = hash;
  }
  named[at] = info.name[0] != 0;
  logf("scan: %02x:%02x:%02x:%02x:%02x:%02x type %u rssi %d name \"%s\" hid %d appearance %04x match %d",
       scan.bd_addr[5], scan.bd_addr[4], scan.bd_addr[3], scan.bd_addr[2], scan.bd_addr[1],
       scan.bd_addr[0], (unsigned)scan.remote_addr_type, scan.rssi, info.name, info.hid,
       info.appearance, match);
}

T_APP_RESULT gapAppHook(uint8_t cbType, void *data) {
  // The library keeps a 764-byte record of every device it's shown, and a busy room fills the
  // heap in under a second. It's only shown the keyboard.
  if (cbType == GAP_MSG_LE_SCAN_INFO) {
    const T_LE_SCAN_INFO &scan = *((T_LE_CB_DATA *)data)->p_le_scan_info;
    AdvInfo info;
    parseAdv(scan.data, scan.data_len > sizeof(scan.data) ? sizeof(scan.data) : scan.data_len, info);
    const bool match = advIsKeyboard(info);
    logDevice(scan, info, match);
    if (!match || keyboardDevice != nullptr) return APP_RESULT_SUCCESS;
  }
  enterHandler(EVENT_GAP_APP, cbType);
  const T_APP_RESULT result = libGapAppCallback(cbType, data);
  leaveHandler();
  return result;
}

void gapMsgHook(T_IO_MSG *msg) {
  T_LE_GAP_MSG gap;
  memcpy(&gap, &msg->u.param, sizeof(msg->u.param));
  noteEvent(EVENT_GAP_MSG, msg->subtype);
  switch (msg->subtype) {
    case GAP_MSG_LE_CONN_STATE_CHANGE: {
      const T_GAP_CONN_STATE_CHANGE &s = gap.msg_data.gap_conn_state_change;
      logf("ble: conn state %u, id %u, cause 0x%x", s.new_state, s.conn_id, s.disc_cause);
      if (s.new_state == GAP_CONN_STATE_CONNECTED) {
        linkConnId = s.conn_id;
        linkUp = true;
        crumb->link = 1;
      } else if (s.new_state == GAP_CONN_STATE_DISCONNECTED && linkUp) {
        linkUp = false;
        linkLost = true;
        crumb->link = 2;
      }
      break;
    }
    case GAP_MSG_LE_AUTHEN_STATE_CHANGE: {
      const T_GAP_AUTHEN_STATE &a = gap.msg_data.gap_authen_state;
      logf("ble: auth state %u, status 0x%x", a.new_state, a.status);
      if (a.new_state == GAP_AUTHEN_STATE_COMPLETE) {
        authStatus = a.status;
        authDone = true;
      }
      break;
    }
    // The library confirms these from inside the callback (an RPC call, see above), and gets
    // the passkey case wrong, so handle them ourselves on the main thread.
    case GAP_MSG_LE_BOND_PASSKEY_DISPLAY:
      logf("ble: passkey requested");
      pendingBondConnId = gap.msg_data.gap_bond_passkey_display.conn_id;
      pendingBondMsg = msg->subtype;
      return;
    case GAP_MSG_LE_BOND_JUST_WORK:
      logf("ble: just-works pairing requested");
      pendingBondConnId = gap.msg_data.gap_bond_just_work_conf.conn_id;
      pendingBondMsg = msg->subtype;
      return;
    case GAP_MSG_LE_BOND_USER_CONFIRMATION:
      logf("ble: numeric comparison requested");
      pendingBondConnId = gap.msg_data.gap_bond_user_conf.conn_id;
      pendingBondMsg = msg->subtype;
      return;
    case GAP_MSG_LE_DEV_STATE_CHANGE:
      break;
    default:
      logf("ble: gap msg 0x%02x", msg->subtype);
      break;
  }
  // Only device state goes on to the library. Its other handlers aren't needed here, and its
  // connection-update handler makes RPC calls from this thread, which hangs the link for good.
  if (msg->subtype == GAP_MSG_LE_DEV_STATE_CHANGE) {
    enterHandler(EVENT_GAP_MSG, msg->subtype);
    libGapMsgHandler(msg);
    leaveHandler();
  }
}

class KeyboardFinder : public BLEAdvertisedDeviceCallbacks {
  void onResult(BLEAdvertisedDevice dev) override {
    if (keyboardDevice != nullptr) return;
    // No scan->stop() here (it's an RPC call); the current scan window just runs out.
    keyboardDevice = new BLEAdvertisedDevice(dev);
    logf("scan: picked %s \"%s\"", dev.getAddress().toString().c_str(), dev.getName().c_str());
  }
};

// Restarts the Wio if a Bluetooth call never returns while connecting, about 16 s without a feed.
void feedWatchdog() {
  if (!WDT->CTRLA.bit.ENABLE) return;
  while (WDT->SYNCBUSY.reg) {
  }
  WDT->CLEAR.reg = WDT_CLEAR_CLEAR_KEY;
}

struct ConnectWatchdog {
  ConnectWatchdog() {
    MCLK->APBAMASK.bit.WDT_ = 1;
    WDT->CTRLA.reg = 0;
    while (WDT->SYNCBUSY.reg) {
    }
    WDT->CONFIG.reg = WDT_CONFIG_PER_CYC16384;
    WDT->CTRLA.reg = WDT_CTRLA_ENABLE;
    while (WDT->SYNCBUSY.reg) {
    }
  }
  ~ConnectWatchdog() {
    if (crumb->magic == CRUMB_MAGIC) logf("ble: connection attempt failed at step %s", STEP_NAMES[crumb->step]);
    crumb->magic = 0;  // a hang never gets here, so its details survive the reset
    while (WDT->SYNCBUSY.reg) {
    }
    WDT->CTRLA.reg = 0;
    while (WDT->SYNCBUSY.reg) {
    }
  }
};

bool waitFor(volatile bool &flag, uint32_t timeoutMs) {
  const uint32_t start = millis();
  while (!flag && millis() - start < timeoutMs) {
    feedWatchdog();
    delay(10);
  }
  return flag;
}

bool writeDescriptor(uint16_t handle, uint8_t *value, uint16_t len) {
  writeDone = false;
  if (client_attr_write(linkConnId, client->getGattcIf(), GATT_WRITE_TYPE_REQ, handle, len, value) !=
      GAP_CAUSE_SUCCESS) {
    return false;
  }
  return waitFor(writeDone, 3000) && writeCause == GAP_SUCCESS;
}

// Writes the LED state to every writable, non-notifying report.
void setKeyboardLeds(uint8_t leds) {
  for (uint8_t i = 0; i < charCount; i++) {
    const GattChar &c = chars[i];
    const bool isOutput = c.uuid == UUID_BOOT_KEYBOARD_OUTPUT ||
                          (c.uuid == UUID_REPORT && (c.properties & CHAR_PROP_WRITE) &&
                           !(c.properties & CHAR_PROP_NOTIFY));
    if (!isOutput) continue;
    writeDone = false;
    if (client_attr_write(linkConnId, client->getGattcIf(), GATT_WRITE_TYPE_REQ, c.valueHandle, 1,
                          &leds) == GAP_CAUSE_SUCCESS) {
      waitFor(writeDone, 1000);
    }
  }
}

bool awaitDiscovery(const char *what, T_GAP_CAUSE cause) {
  if (cause != GAP_CAUSE_SUCCESS) {
    logf("ble: %s discovery request failed: 0x%x", what, cause);
    return false;
  }
  const uint32_t start = millis();
  while (discoveryState == DISC_PENDING && millis() - start < 5000) {
    feedWatchdog();
    delay(10);
  }
  if (discoveryState != DISC_DONE) {
    logf("ble: %s discovery %s", what, discoveryState == DISC_FAILED ? "failed" : "timed out");
    return false;
  }
  return true;
}

uint16_t findCccd(uint8_t index, uint16_t serviceEnd) {
  const GattChar &c = chars[index];
  const uint16_t end = index + 1 < charCount ? chars[index + 1].declHandle - 1 : serviceEnd;
  if (end <= c.valueHandle) return 0;
  descCount = 0;
  discoveryState = DISC_PENDING;
  if (!awaitDiscovery("Descriptor", client_all_char_descriptor_discovery(linkConnId, client->getGattcIf(),
                                                                         c.valueHandle + 1, end))) {
    return 0;
  }
  for (uint8_t i = 0; i < descCount; i++) {
    if (descs[i].uuid == UUID_CCCD) return descs[i].handle;
  }
  return 0;
}

bool subscribe(uint8_t index, uint16_t serviceEnd) {
  const uint16_t valueHandle = chars[index].valueHandle;
  const uint16_t cccd = findCccd(index, serviceEnd);
  if (cccd == 0 || inputHandleCount >= MAX_INPUT_HANDLES) return false;
  // Register the handle first so the first notification isn't dropped.
  inputHandles[inputHandleCount++] = valueHandle;
  uint8_t enableNotify[] = {0x01, 0x00};
  const bool ok = writeDescriptor(cccd, enableNotify, sizeof(enableNotify));
  if (!ok) inputHandleCount--;
  return ok;
}

void serviceBondRequest(StatusFn status) {
  const uint16_t msg = pendingBondMsg;
  if (msg == 0) return;
  const uint8_t connId = pendingBondConnId;
  pendingBondMsg = 0;
  switch (msg) {
    case GAP_MSG_LE_BOND_PASSKEY_DISPLAY: {
      uint32_t key = 0;
      le_bond_get_display_key(connId, &key);
      char code[16];
      snprintf(code, sizeof(code), "%06lu", (unsigned long)key);
      if (status) status(code, "Type this code on the keyboard, then Enter");
      le_bond_passkey_display_confirm(connId, GAP_CFM_CAUSE_ACCEPT);
      break;
    }
    case GAP_MSG_LE_BOND_JUST_WORK:
      le_bond_just_work_confirm(connId, GAP_CFM_CAUSE_ACCEPT);
      break;
    case GAP_MSG_LE_BOND_USER_CONFIRMATION:
      le_bond_user_confirm(connId, GAP_CFM_CAUSE_ACCEPT);
      break;
  }
}

bool contains(const uint8_t *keys, uint8_t usage) {
  for (int i = 0; i < 6; i++) {
    if (keys[i] == usage) return true;
  }
  return false;
}

void handleReport(const Report &r) {
  // Boot keyboard layout: [modifiers, reserved, key1..key6]. Some report-mode keyboards omit
  // the reserved byte (7 bytes). Anything else (media keys, NKRO bitmaps, mice) is ignored.
  const uint8_t *keys;
  if (r.len == 8) keys = r.data + 2;
  else if (r.len == 7) keys = r.data + 1;
  else return;
  const uint8_t modifiers = r.data[0];
  if (keys[0] == HID_USAGE_ROLLOVER) return;

  for (int i = 0; i < 6; i++) {
    const uint8_t usage = keys[i];
    if (usage == 0 || contains(prevKeys, usage)) continue;
    if (usage == HID_USAGE_CAPS_LOCK) {
      capsLock = !capsLock;
      setKeyboardLeds(capsLock ? HID_LED_CAPS_LOCK : 0);
      continue;
    }
    Input in;
    if (!hidToInput(usage, modifiers, capsLock, in)) continue;
    pushInput(in);
    repeatInput = in;
    repeatInput.repeat = true;
    repeatUsage = usage;
    repeatAt = millis() + REPEAT_DELAY_MS;
  }
  if (repeatUsage && !contains(keys, repeatUsage)) repeatUsage = 0;
  memcpy(prevKeys, keys, sizeof(prevKeys));
}

}  // namespace

bool begin() {
  const char *firmware = rpc_system_version();
  logf("ble: RTL8720 firmware %s", firmware);
  const bool canBond = strstr(firmware, "-ftl") != nullptr;

  // If this never returns, the RTL8720 wireless firmware is missing or out of date.
  BLEDevice::init("Wio Terminal");
  libGattcCallback = _ble_gattc_callback;
  le_register_gattc_cb(gattcHook);
  libGapMsgHandler = _handle_gap_msg;
  le_register_msg_handler(gapMsgHook);
  libGapAppCallback = _ble_gap_callback;
  le_register_app_cb(gapAppHook);

  // Advertise a display so keyboards that require MITM protection can use passkey entry; the
  // rest fall back to "Just Works".
  uint8_t ioCap = GAP_IO_CAP_DISPLAY_ONLY;
  uint16_t authFlags = GAP_AUTHEN_BIT_BONDING_FLAG;
  gap_set_param(GAP_PARAM_BOND_IO_CAPABILITIES, sizeof(ioCap), &ioCap);
  gap_set_param(GAP_PARAM_BOND_AUTHEN_REQUIREMENTS_FLAGS, sizeof(authFlags), &authFlags);
  return canBond;
}

void clearBonds() { le_bond_clear_all_keys(); }

bool hungLastBoot() { return RSTC->RCAUSE.reg & RSTC_RCAUSE_WDT; }

void logProgress() {
  if (crumb->magic != CRUMB_MAGIC) return;
  char report[64];
  hangReport(report, sizeof(report));
  logf("ble: alive, at %s", report);
}

void hangReport(char *out, size_t cap) {
  if (crumb->magic != CRUMB_MAGIC || crumb->step > STEP_LEDS || crumb->handlerKind > EVENT_GATT ||
      crumb->lastKind > EVENT_GATT) {
    snprintf(out, cap, "no details saved");
    return;
  }
  static const char *const links[] = {"no link", "link up", "link lost"};
  snprintf(out, cap, "%s, %s, in %s %02x, last %s %02x", STEP_NAMES[crumb->step],
           links[crumb->link > 2 ? 0 : crumb->link], EVENT_NAMES[crumb->handlerKind],
           crumb->handlerType, EVENT_NAMES[crumb->lastKind], crumb->lastType);
}

bool connect(StatusFn status, CancelFn cancel) {
  crumb->magic = CRUMB_MAGIC;
  crumb->link = 0;
  crumb->handlerKind = EVENT_NONE;
  crumb->handlerType = 0;
  crumb->lastKind = EVENT_NONE;
  crumb->lastType = 0;
  setStep(STEP_SCAN);
  status("Keyboard", "Put it in pairing mode. Hold C to skip.");
  BLEScan *scan = BLEDevice::getScan();
  scan->setAdvertisedDeviceCallbacks(new KeyboardFinder());
  scan->setInterval(1349);
  scan->setWindow(449);
  scan->setActiveScan(true);  // keyboards usually put their name in the scan response
  uint32_t rounds = 0;
  while (keyboardDevice == nullptr) {
    if (cancel()) {
      logf("ble: scan skipped");
      crumb->magic = 0;
      return false;
    }
    logf("ble: scanning, round %lu", (unsigned long)++rounds);
    scan->start(2, false);
  }

  setStep(STEP_CREATE);
  ConnectWatchdog watchdog;
  std::string name = keyboardDevice->getName();
  if (name.empty()) name = keyboardDevice->getAddress().toString();
  status("Connecting", name.c_str());
  client = BLEDevice::createClient();
  setStep(STEP_LIB_CONNECT);
  client->connect(keyboardDevice);
  setStep(STEP_WAIT_LINK);
  // GATT calls use linkConnId (from the connect event) rather than client->getConnId(), which
  // BLEClient looks up assuming a public address and gets wrong for random-address keyboards.
  if (!waitFor(linkUp, 5000)) return false;

  // For a keyboard we're already bonded with, this just re-encrypts with the stored keys.
  status("Pairing", "Accept on the keyboard if asked");
  authDone = false;
  setStep(STEP_BOND);
  le_bond_pair(linkConnId);
  setStep(STEP_WAIT_AUTH);
  const uint32_t start = millis();
  while (!authDone && millis() - start < 20000) {
    feedWatchdog();
    serviceBondRequest(status);
    if (linkLost) return false;
    delay(20);
  }
  if (!(authDone && authStatus == GAP_SUCCESS)) {
    // Usually a stale bond on one side. Carry on; if keys don't arrive, forget keyboards.
    logf("ble: pairing failed, status 0x%x", authStatus);
  }

  status("Keyboard", "Reading its services");
  setStep(STEP_SERVICES);
  serviceCount = 0;
  discoveryState = DISC_PENDING;
  if (!awaitDiscovery("Service", client_all_primary_srv_discovery(linkConnId, client->getGattcIf()))) {
    return false;
  }
  const GattService *hid = nullptr;
  for (uint8_t i = 0; i < serviceCount; i++) {
    if (services[i].uuid == UUID_HID_SERVICE) hid = &services[i];
  }
  if (hid == nullptr) return false;

  setStep(STEP_CHARS);
  charCount = 0;
  discoveryState = DISC_PENDING;
  if (!awaitDiscovery("Characteristic",
                      client_all_char_discovery(linkConnId, client->getGattcIf(), hid->start, hid->end))) {
    return false;
  }

  int bootInput = -1, protocolMode = -1;
  for (uint8_t i = 0; i < charCount; i++) {
    if (chars[i].uuid == UUID_BOOT_KEYBOARD_INPUT) bootInput = i;
    if (chars[i].uuid == UUID_PROTOCOL_MODE) protocolMode = i;
  }
  bool subscribed = false;
  if (bootInput >= 0 && protocolMode >= 0) {
    // Boot protocol gives a fixed 8-byte report, so no report-map parsing is needed.
    setStep(STEP_PROTOCOL);
    uint8_t mode = PROTOCOL_MODE_BOOT;
    client_attr_write(linkConnId, client->getGattcIf(), GATT_WRITE_TYPE_CMD,
                      chars[protocolMode].valueHandle, 1, &mode);
    setStep(STEP_SUBSCRIBE);
    subscribed = subscribe(bootInput, hid->end);
  }
  // Also subscribe to the report-mode inputs, in case the keyboard ignores the protocol mode
  // switch. Non-keyboard reports (media keys, mouse) are filtered out by length later.
  for (uint8_t i = 0; i < charCount; i++) {
    if (chars[i].uuid == UUID_REPORT && (chars[i].properties & CHAR_PROP_NOTIFY)) {
      subscribed |= subscribe(i, hid->end);
    }
  }
  if (!subscribed) return false;
  setStep(STEP_LEDS);
  setKeyboardLeds(0);
  crumb->magic = 0;
  logf("ble: keyboard ready");
  ready = true;
  return true;
}

bool poll(Input &out) {
  if (!ready) return false;
  serviceBondRequest(nullptr);
  Report r;
  while (dequeueReport(r)) {
    if (isInputHandle(r.handle)) handleReport(r);
  }
  const uint32_t now = millis();
  if (repeatUsage && !linkLost && (int32_t)(now - repeatAt) >= 0) {
    pushInput(repeatInput);
    repeatAt = now + REPEAT_RATE_MS;
  }
  if (linkLost) repeatUsage = 0;
  if (inputTail == inputHead) return false;
  out = inputs[inputTail];
  inputTail = (inputTail + 1) % INPUT_QUEUE;
  return true;
}

KeyboardState state() {
  if (!ready) return KB_NONE;
  return linkLost ? KB_LOST : KB_CONNECTED;
}

}  // namespace BleKeyboard
