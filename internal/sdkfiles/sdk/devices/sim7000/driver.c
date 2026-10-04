/* SIMCom SIM7000 modem — SMS (with delivery receipts) and HTTP over GPRS,
 * with retries, soft reset, PWRKEY power-cycling and automatic recovery. */

#include "volt/sensor.h"
#include "volt/uart.h"
#include "volt/pin.h"
#include "volt/task.h"
#include "volt/log.h"
#include "volt/storage.h"
#include <string.h>
#include <stdio.h>
#include <stdlib.h>
#ifdef ESP_PLATFORM
#include "esp_task_wdt.h"
#endif

#define SIM7000_RESP_MAX 256
#define SIM7000_APN "econet.net"
#define SIM7000_UNREG_RECOVER_MS (180u * 1000u)   // off-network this long → recover

// ---- low level -------------------------------------------------------------

// Long waits must not starve the task watchdog (the scheduler's task is
// subscribed to it); before the scheduler subscribes, do nothing.
static void sim7000_feed(void)
{
#ifdef ESP_PLATFORM
    if (esp_task_wdt_status(NULL) == ESP_OK) esp_task_wdt_reset();
#endif
}

static void sim7000_sleep(uint32_t ms)
{
    while (ms > 0) {
        uint32_t step = ms > 200 ? 200 : ms;
        sim7000_feed();
        volt_delay_ms(step);
        ms -= step;
    }
}

// One-line printable copy of a modem reply for logs.
static const char *sim7000_flat(const char *s)
{
    static char out[SIM7000_RESP_MAX];
    int j = 0;
    for (int i = 0; s[i] && j < SIM7000_RESP_MAX - 1; i++) out[j++] = (s[i] == '\r' || s[i] == '\n') ? ' ' : s[i];
    out[j] = '\0';
    return out;
}

static void sim7000_drain(sim7000_t *dev)
{
    while (volt_uart_read_byte(dev->uart_id) >= 0) {}
}

// Collect bytes until `expect` (or ERROR) appears or the timeout passes.
static bool sim7000_wait_n(sim7000_t *dev, const char *expect, uint32_t timeout_ms, char *resp, int size)
{
    int len = 0;
    resp[0] = '\0';
    uint32_t t0 = volt_tick_ms();
    while (volt_tick_ms() - t0 < timeout_ms) {
        int c = volt_uart_read_byte(dev->uart_id);
        if (c < 0) { sim7000_feed(); volt_delay_ms(5); continue; }
        // Transmit bursts can inject noise bytes on the UART; replies are
        // plain ASCII, so drop anything else instead of filling the buffer.
        if (c == 0 || c >= 0x80) continue;
        if (len < size - 1) { resp[len++] = (char)c; resp[len] = '\0'; }
        if (strstr(resp, expect)) return true;
        if (strstr(resp, "ERROR")) return false;
    }
    return false;
}

static bool sim7000_wait(sim7000_t *dev, const char *expect, uint32_t timeout_ms, char *resp)
{
    return sim7000_wait_n(dev, expect, timeout_ms, resp, SIM7000_RESP_MAX);
}

// Send a command (stale bytes drained first) and wait for `expect`.
static bool sim7000_cmd(sim7000_t *dev, const char *cmd, const char *expect,
                        uint32_t timeout_ms, char *resp)
{
    sim7000_drain(dev);
    volt_uart_print(dev->uart_id, cmd);
    volt_uart_print(dev->uart_id, "\r");
    return sim7000_wait(dev, expect, timeout_ms, resp);
}

// Cheap liveness check; a few tries since the modem can be busy briefly
// right after transmitting.
static bool sim7000_alive(sim7000_t *dev)
{
    char resp[SIM7000_RESP_MAX];
    for (int i = 0; i < 3; i++) {
        if ((dev->alive = sim7000_cmd(dev, "AT", "OK", 500, resp))) break;
    }
    return dev->alive;
}

// Poll AT until the modem answers (after power-up or a restart).
static bool sim7000_wait_boot(sim7000_t *dev, uint32_t timeout_ms)
{
    char resp[SIM7000_RESP_MAX];
    uint32_t t0 = volt_tick_ms();
    while (volt_tick_ms() - t0 < timeout_ms) {
        if (sim7000_cmd(dev, "AT", "OK", 500, resp)) { dev->alive = true; return true; }
        sim7000_sleep(500);
    }
    dev->alive = false;
    return false;
}

// Settings applied after every (re)start.
static void sim7000_configure(sim7000_t *dev)
{
    char resp[SIM7000_RESP_MAX];
    sim7000_cmd(dev, "ATE0", "OK", 1000, resp);
    sim7000_cmd(dev, "AT+CMEE=2", "OK", 1000, resp);
    sim7000_cmd(dev, "AT+CNMP=13", "OK", 3000, resp);          // GSM only: no LTE-M/NB-IoT wait
    sim7000_cmd(dev, "AT+CMGF=1", "OK", 1000, resp);           // SMS text mode
    sim7000_cmd(dev, "AT+CSCS=\"GSM\"", "OK", 1000, resp);
    sim7000_cmd(dev, "AT+CPMS=\"SM\",\"SM\",\"SM\"", "OK", 2000, resp);  // read/store incoming on SIM
    sim7000_cmd(dev, "AT+CSMP=49,167,0,0", "OK", 1000, resp);  // request delivery receipts
    sim7000_cmd(dev, "AT+CNMI=1,1,0,1,0", "OK", 1000, resp);   // receipts arrive as +CDS lines
    sim7000_cmd(dev, "AT+CLTS=1", "OK", 1000, resp);           // take time from the network
    dev->last_reg_ms = volt_tick_ms();
}

// ---- lifecycle ---------------------------------------------------------------

volt_error_t sim7000_init(sim7000_t *dev, uint8_t uart_id)
{
    memset(dev, 0, sizeof(*dev));
    dev->uart_id = uart_id;
    dev->last_ref = -1;
    dev->pwrkey = -1;
    if (!sim7000_wait_boot(dev, 5000)) {   // auto-baud boards need a few ATs
        log_error("sim7000", "no response on uart %d (check supply, GND, T/R)", uart_id);
        return VOLT_ERR_INTERNAL;
    }
    sim7000_configure(dev);
    log_info("sim7000", "modem ready on uart %d", uart_id);
    return VOLT_OK;
}

void sim7000_set_pwrkey(sim7000_t *dev, int32_t pin, bool active_high)
{
    dev->pwrkey = (int16_t)pin;
    dev->pwr_active_high = active_high;
    pin_set_input((uint8_t)pin);   // released: the board's own pull sets the idle level
}

static void sim7000_press(sim7000_t *dev, uint32_t ms)
{
    uint8_t pin = (uint8_t)dev->pwrkey;
    pin_set_output(pin);
    pin_write(pin, dev->pwr_active_high ? 1 : 0);
    sim7000_sleep(ms);
    pin_set_input(pin);            // release
}

int32_t sim7000_network_status(sim7000_t *dev);

// After a restart, AT answers within seconds but the network takes longer;
// wait for full registration (status 1/5) so the next transaction can work.
static bool sim7000_wait_registered(sim7000_t *dev, uint32_t timeout_ms)
{
    uint32_t t0 = volt_tick_ms();
    while (volt_tick_ms() - t0 < timeout_ms) {
        int32_t st = sim7000_network_status(dev);
        if (st == 1 || st == 5) return true;
        sim7000_sleep(1000);
    }
    log_error("sim7000", "not registered %u s after restart", (unsigned)(timeout_ms / 1000));
    return false;
}

bool sim7000_reset(sim7000_t *dev)
{
    char resp[SIM7000_RESP_MAX];
    log_info("sim7000", "soft reset");
    if (!sim7000_cmd(dev, "AT+CFUN=1,1", "OK", 5000, resp)) return false;
    sim7000_sleep(3000);
    if (!sim7000_wait_boot(dev, 30000)) { log_error("sim7000", "no answer after soft reset"); return false; }
    sim7000_configure(dev);
    bool reg = sim7000_wait_registered(dev, 60000);
    log_info("sim7000", "soft reset done (%s)", reg ? "registered" : "not registered yet");
    return true;
}

bool sim7000_power_cycle(sim7000_t *dev)
{
    if (dev->pwrkey < 0) { log_error("sim7000", "power_cycle: no PWRKEY pin set"); return false; }
    log_info("sim7000", "power cycle via PWRKEY (GPIO %d)", dev->pwrkey);
    if (sim7000_alive(dev)) {           // on → press to switch off, wait for it to go quiet
        sim7000_press(dev, 1500);
        uint32_t t0 = volt_tick_ms();
        while (volt_tick_ms() - t0 < 10000 && sim7000_alive(dev)) sim7000_sleep(500);
        sim7000_sleep(2000);
    }
    sim7000_press(dev, 1200);           // off → press to switch on
    if (!sim7000_wait_boot(dev, 30000)) { log_error("sim7000", "no answer after power cycle"); return false; }
    sim7000_configure(dev);
    bool reg = sim7000_wait_registered(dev, 60000);
    log_info("sim7000", "power cycle done (%s)", reg ? "registered" : "not registered yet");
    return true;
}

// Escalate: modem answers → soft reset; silent → power-cycle.
bool sim7000_recover(sim7000_t *dev)
{
    log_info("sim7000", "recovering modem");
    bool ok = sim7000_alive(dev) ? sim7000_reset(dev) : false;
    if (!ok) ok = sim7000_power_cycle(dev);
    if (!ok) log_error("sim7000", "recovery failed");
    return ok;
}

// Make sure the modem is usable before a transaction (one recovery attempt).
static bool sim7000_usable(sim7000_t *dev)
{
    if (sim7000_alive(dev)) return true;
    log_error("sim7000", "modem not answering");
    return sim7000_recover(dev);
}

// ---- status ----------------------------------------------------------------

bool sim7000_ready(sim7000_t *dev)
{
    return sim7000_alive(dev);
}

// +CREG <stat>: 0 not searching, 1 home, 2 searching, 3 denied, 4 unknown, 5 roaming; -1 no answer
int32_t sim7000_network_status(sim7000_t *dev)
{
    char resp[SIM7000_RESP_MAX];
    if (!sim7000_cmd(dev, "AT+CREG?", "OK", 1000, resp)) return -1;
    const char *p = strstr(resp, "+CREG:");
    if (!p || !(p = strchr(p, ','))) return -1;
    int stat = atoi(p + 1);
    if (stat == 1 || stat == 5) dev->last_reg_ms = volt_tick_ms();
    return stat;
}

bool sim7000_registered(sim7000_t *dev)
{
    int32_t s = sim7000_network_status(dev);
    return s == 1 || s == 5;
}

int32_t sim7000_signal(sim7000_t *dev)
{
    char resp[SIM7000_RESP_MAX];
    if (!sim7000_cmd(dev, "AT+CSQ", "OK", 1000, resp)) return 99;
    const char *p = strstr(resp, "+CSQ:");
    int rssi = 99;
    if (p) sscanf(p, "+CSQ: %d", &rssi);
    return rssi;
}

// CSQ → dBm (-113 .. -51); 0 when unknown.
int32_t sim7000_signal_dbm(sim7000_t *dev)
{
    int32_t csq = sim7000_signal(dev);
    return (csq < 0 || csq > 31) ? 0 : -113 + 2 * csq;
}

const char *sim7000_operator_name(sim7000_t *dev)
{
    static char name[32];
    char resp[SIM7000_RESP_MAX];
    name[0] = '\0';
    if (!sim7000_cmd(dev, "AT+COPS?", "OK", 2000, resp)) return name;
    const char *q = strchr(resp, '"');
    if (!q) return name;
    const char *e = strchr(q + 1, '"');
    int n = e ? (int)(e - q - 1) : 0;
    if (n > (int)sizeof(name) - 1) n = sizeof(name) - 1;
    memcpy(name, q + 1, n);
    name[n] = '\0';
    return name;
}

// Supply voltage seen by the modem, in millivolts (0 if unknown).
int32_t sim7000_voltage(sim7000_t *dev)
{
    char resp[SIM7000_RESP_MAX];
    if (!sim7000_cmd(dev, "AT+CBC", "OK", 1000, resp)) return 0;
    const char *p = strstr(resp, "+CBC:");
    int bcs = 0, bcl = 0, mv = 0;
    if (p) sscanf(p, "+CBC: %d,%d,%d", &bcs, &bcl, &mv);
    return mv;
}

// Days since 1970-01-01 for a civil date (Howard Hinnant's algorithm).
static int32_t sim7000_days(int y, int m, int d)
{
    y -= m <= 2;
    int era = (y >= 0 ? y : y - 399) / 400;
    int yoe = y - era * 400;
    int doy = (153 * (m + (m > 2 ? -3 : 9)) + 2) / 5 + d - 1;
    int doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    return era * 146097 + doe - 719468;
}

// Network time as Unix seconds (UTC); 0 until the network has sent the time.
int32_t sim7000_network_time(sim7000_t *dev)
{
    char resp[SIM7000_RESP_MAX];
    if (!sim7000_cmd(dev, "AT+CCLK?", "OK", 1000, resp)) return 0;
    const char *p = strstr(resp, "+CCLK: \"");
    int yy, MM, dd, hh, mm, ss, tz = 0;
    char sign = '+';
    if (!p || sscanf(p, "+CCLK: \"%d/%d/%d,%d:%d:%d%c%d", &yy, &MM, &dd, &hh, &mm, &ss, &sign, &tz) < 6) return 0;
    if (yy < 24 || yy > 79) return 0;   // modem default (no network time yet)
    int32_t t = sim7000_days(2000 + yy, MM, dd) * 86400 + hh * 3600 + mm * 60 + ss;
    int32_t off = tz * 15 * 60;          // tz is in quarter hours of local time
    return sign == '-' ? t + off : t - off;
}

// Call periodically: refreshes registration and recovers the modem if it has
// been off the network for too long. Returns the network status.
int32_t sim7000_maintain(sim7000_t *dev)
{
    int32_t stat = sim7000_network_status(dev);
    if (stat == -1 && sim7000_alive(dev)) stat = sim7000_network_status(dev);   // was just busy
    if (stat == -1 && !sim7000_alive(dev)) {
        sim7000_recover(dev);
        return sim7000_network_status(dev);
    }
    if (stat != 1 && stat != 5 && volt_tick_ms() - dev->last_reg_ms > SIM7000_UNREG_RECOVER_MS) {
        log_error("sim7000", "off network for %u s (status %d)", (unsigned)(SIM7000_UNREG_RECOVER_MS / 1000), (int)stat);
        sim7000_recover(dev);
        dev->last_reg_ms = volt_tick_ms();   // give it another window before retrying
        stat = sim7000_network_status(dev);
    }
    return stat;
}

// ---- SMS -----------------------------------------------------------------------

// One attempt: 1 sent, 0 refused before sending (safe to retry),
// -1 outcome unknown (may have been sent; never retried).
static int sim7000_sms_once(sim7000_t *dev, const char *number, const char *text)
{
    char resp[SIM7000_RESP_MAX];
    char cmd[48];
    snprintf(cmd, sizeof(cmd), "AT+CMGS=\"%s\"", number);
    if (!sim7000_cmd(dev, cmd, ">", 5000, resp)) {
        log_error("sim7000", "no SMS prompt: [%s]", sim7000_flat(resp));
        volt_uart_print(dev->uart_id, "\x1b");   // leave prompt mode if half-entered
        return 0;
    }
    volt_uart_print(dev->uart_id, text);
    volt_uart_print(dev->uart_id, "\x1a");      // Ctrl-Z sends
    bool done = sim7000_wait(dev, "OK", 30000, resp);
    const char *ref = strstr(resp, "+CMGS:");
    if (done && ref) {
        dev->last_ref = atoi(ref + 6);
        dev->receipt = 0;
        log_info("sim7000", "SMS to %s accepted (ref %d)", number, (int)dev->last_ref);
        return 1;
    }
    if (strstr(resp, "+CMS ERROR")) {           // network/modem refused it: nothing was sent
        // The reader stops at "ERROR"; collect the rest of the line (the code).
        int l = (int)strlen(resp);
        sim7000_wait_n(dev, "\n", 500, resp + l, SIM7000_RESP_MAX - l);
        log_error("sim7000", "SMS refused: [%s]", sim7000_flat(resp));
        return 0;
    }
    log_error("sim7000", "SMS outcome unknown: [%s]", sim7000_flat(resp));
    return -1;
}

bool sim7000_send_sms(sim7000_t *dev, const char *number, const char *text)
{
    static const uint32_t backoff_ms[] = {2000, 5000};
    for (int attempt = 0; attempt < 3; attempt++) {
        if (!sim7000_usable(dev)) return false;
        int r = sim7000_sms_once(dev, number, text);
        if (r == 1) return true;
        if (r < 0) return false;                // might have gone out — don't double-send
        if (attempt < 2) {
            log_info("sim7000", "SMS retry %d in %u ms", attempt + 1, (unsigned)backoff_ms[attempt]);
            sim7000_sleep(backoff_ms[attempt]);
        }
    }
    return false;
}

// Scan pending modem output for the delivery receipt of the last SMS.
// Text-mode +CDS: <fo>,<mr>,"<ra>",<tora>,"<scts>","<dt>",<st>  (st 0 = delivered)
int32_t sim7000_check_receipt(sim7000_t *dev)
{
    if (dev->receipt != 0 || dev->last_ref < 0) return dev->receipt;
    char buf[SIM7000_RESP_MAX];
    int len = 0;
    int c;
    while (len < SIM7000_RESP_MAX - 1 && (c = volt_uart_read_byte(dev->uart_id)) >= 0) buf[len++] = (char)c;
    buf[len] = '\0';
    const char *p = strstr(buf, "+CDS:");
    if (!p) return 0;
    int fo = 0, mr = -1;
    if (sscanf(p, "+CDS: %d,%d", &fo, &mr) != 2 || mr != dev->last_ref) return 0;
    const char *st = strrchr(p, ',');   // status is the last field
    int status = st ? atoi(st + 1) : -1;
    dev->receipt = (status == 0) ? 1 : (status >= 64 ? -1 : 0);
    log_info("sim7000", "receipt for ref %d: status %d", mr, status);
    return dev->receipt;
}

// ---- HTTP over GPRS ------------------------------------------------------------

// Traced command for the HTTP path: logs each reply (debug level).
static bool sim7000_tcmd(sim7000_t *dev, const char *cmd, const char *expect, uint32_t timeout_ms, char *resp)
{
    bool ok = sim7000_cmd(dev, cmd, expect, timeout_ms, resp);
    log_debug("sim7000", "%s -> %s [%s]", cmd, ok ? "ok" : "FAIL", sim7000_flat(resp));
    return ok;
}

static int32_t sim7000_http_once(sim7000_t *dev, const char *url, const char *body)
{
    char resp[SIM7000_RESP_MAX];
    char cmd[200];

    // GPRS bearer (kept open between posts)
    if (!sim7000_tcmd(dev, "AT+SAPBR=2,1", "OK", 3000, resp) || !strstr(resp, "+SAPBR: 1,1")) {
        sim7000_tcmd(dev, "AT+SAPBR=3,1,\"CONTYPE\",\"GPRS\"", "OK", 2000, resp);
        sim7000_tcmd(dev, "AT+SAPBR=3,1,\"APN\",\"" SIM7000_APN "\"", "OK", 2000, resp);
        if (!sim7000_tcmd(dev, "AT+SAPBR=1,1", "OK", 30000, resp)) {
            log_error("sim7000", "GPRS bearer failed: [%s]", sim7000_flat(resp));
            return -1;
        }
    }

    sim7000_cmd(dev, "AT+HTTPTERM", "OK", 2000, resp);   // clear any previous session
    if (!sim7000_tcmd(dev, "AT+HTTPINIT", "OK", 5000, resp)) return -1;
    sim7000_tcmd(dev, "AT+HTTPPARA=\"CID\",1", "OK", 2000, resp);
    snprintf(cmd, sizeof(cmd), "AT+HTTPPARA=\"URL\",\"%s\"", url);
    sim7000_tcmd(dev, cmd, "OK", 2000, resp);
    sim7000_tcmd(dev, "AT+HTTPPARA=\"CONTENT\",\"application/x-www-form-urlencoded\"", "OK", 2000, resp);

    int n = (int)strlen(body);
    snprintf(cmd, sizeof(cmd), "AT+HTTPDATA=%d,10000", n);
    int32_t status = -1;
    if (sim7000_tcmd(dev, cmd, "DOWNLOAD", 5000, resp)) {
        // The modem drops data sent while it is still finishing "DOWNLOAD\r\n".
        volt_delay_ms(100);
        sim7000_drain(dev);
        volt_uart_print(dev->uart_id, body);   // volt_uart_write's data never arrived here
        bool up = sim7000_wait(dev, "OK", 3000, resp);
        log_debug("sim7000", "HTTPDATA upload %s: [%s]", up ? "ok" : "FAILED", sim7000_flat(resp));
        // +HTTPACTION: <method>,<status>,<length>
        char tail[64];
        if (sim7000_tcmd(dev, "AT+HTTPACTION=1", "+HTTPACTION:", 20000, resp) &&
            sim7000_wait(dev, "\n", 2000, tail)) {
            int method = 0, code = -1;   // tail holds " <method>,<status>,<length>"
            if (sscanf(tail, " %d,%d", &method, &code) == 2) status = code;
        } else {
            log_error("sim7000", "HTTPACTION: [%s]", sim7000_flat(resp));
        }
    } else {
        log_error("sim7000", "HTTPDATA refused: [%s]", sim7000_flat(resp));
    }
    sim7000_cmd(dev, "AT+HTTPTERM", "OK", 2000, resp);
    return status;
}

// POST with up to 3 attempts (2 s, 5 s back-off) on failure or 5xx.
int32_t sim7000_http_post(sim7000_t *dev, const char *url, const char *body)
{
    static const uint32_t backoff_ms[] = {2000, 5000};
    int32_t status = -1;
    for (int attempt = 0; attempt < 3; attempt++) {
        if (!sim7000_usable(dev)) return -1;
        status = sim7000_http_once(dev, url, body);
        if (status >= 0 && status < 500) break;
        if (attempt < 2) {
            log_info("sim7000", "HTTP retry %d in %u ms (status %d)", attempt + 1, (unsigned)backoff_ms[attempt], (int)status);
            sim7000_sleep(backoff_ms[attempt]);
        }
    }
    if (status < 0) log_error("sim7000", "HTTP POST failed");
    else log_info("sim7000", "HTTP POST %s -> %d", url, (int)status);
    return status;
}

// ---- USSD ---------------------------------------------------------------------

static int sim7000_hexval(char c)
{
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    return -1;
}

// UCS-2 hex ("00420061006C") → ASCII in place; non-ASCII becomes '?'.
static void sim7000_ucs2_to_ascii(char *s)
{
    int n = (int)strlen(s), j = 0;
    for (int i = 0; i + 3 < n; i += 4) {
        int v = 0;
        for (int k = 0; k < 4; k++) {
            int h = sim7000_hexval(s[i + k]);
            if (h < 0) return;   // not hex after all — leave untouched
            v = v * 16 + h;
        }
        s[j++] = (v >= 32 && v < 127) || v == '\n' ? (char)v : '?';
    }
    s[j] = '\0';
}

// Send a USSD code (e.g. "*125#") and return the network's reply text
// ("" on failure). Any menu the network opens is closed afterwards.
const char *sim7000_ussd(sim7000_t *dev, const char *code)
{
    static char reply[200];
    static char buf[512];
    char cmd[64];
    reply[0] = '\0';
    if (!sim7000_usable(dev)) return reply;
    snprintf(cmd, sizeof(cmd), "AT+CUSD=1,\"%s\",15", code);
    sim7000_drain(dev);
    volt_uart_print(dev->uart_id, cmd);
    volt_uart_print(dev->uart_id, "\r");
    // OK first, then +CUSD: <m>,"<text>",<dcs> when the network answers
    if (!sim7000_wait_n(dev, "+CUSD:", 20000, buf, sizeof(buf)) ||
        !sim7000_wait_n(dev, "\n", 3000, buf + strlen(buf), (int)(sizeof(buf) - strlen(buf)))) {
        log_error("sim7000", "USSD %s: no reply [%s]", code, sim7000_flat(buf));
        return reply;
    }
    // The text may span lines; read until the closing ",<dcs>" line ends.
    const char *p = strstr(buf, "+CUSD:");
    int m = p ? atoi(p + 6) : 2;
    const char *q = p ? strchr(p, '"') : NULL;
    if (q) {
        const char *e = strrchr(q + 1, '"');
        if (!e || e == q) {   // closing quote not in yet
            sim7000_wait_n(dev, "\"", 5000, buf + strlen(buf), (int)(sizeof(buf) - strlen(buf)));
            p = strstr(buf, "+CUSD:");
            q = strchr(p, '"');
            e = strrchr(q + 1, '"');
        }
        int n = e && e > q ? (int)(e - q - 1) : 0;
        if (n > (int)sizeof(reply) - 1) n = sizeof(reply) - 1;
        memcpy(reply, q + 1, n);
        reply[n] = '\0';
        int dcs = e ? atoi(e + 2) : 15;
        if (dcs == 72 || dcs == 17) sim7000_ucs2_to_ascii(reply);
    }
    if (m == 1) {   // network left a menu open — close it
        char resp[SIM7000_RESP_MAX];
        sim7000_cmd(dev, "AT+CUSD=2", "OK", 3000, resp);
    }
    log_info("sim7000", "USSD %s -> %s", code, reply);
    return reply;
}

// ---- incoming SMS + commands -------------------------------------------------

#define SIM7000_WL_MAX 6
static char sim7000_wl_num[SIM7000_WL_MAX][24];   // full numbers as given
static bool sim7000_wl_fixed[SIM7000_WL_MAX];     // set in code / alert number: not removable by SMS
static char sim7000_in_from[24];
static char sim7000_in_text[161];
static char sim7000_in_cmd[16];

// Last 9 digits, so "+263776162283", "263776162283" and "0776162283" match.
static void sim7000_tail9(const char *num, char *out)
{
    char digits[24];
    int n = 0;
    for (int i = 0; num[i] && n < (int)sizeof(digits) - 1; i++) if (num[i] >= '0' && num[i] <= '9') digits[n++] = num[i];
    digits[n] = '\0';
    strcpy(out, n > 9 ? digits + n - 9 : digits);
}

static int sim7000_wl_find(const char *number)
{
    char t[16], u[16];
    sim7000_tail9(number, t);
    for (int i = 0; i < SIM7000_WL_MAX; i++) {
        if (!sim7000_wl_num[i][0]) continue;
        sim7000_tail9(sim7000_wl_num[i], u);
        if (strcmp(t, u) == 0) return i;
    }
    return -1;
}

static int sim7000_wl_add(const char *number, bool fixed)
{
    int i = sim7000_wl_find(number);
    if (i >= 0) { if (fixed) sim7000_wl_fixed[i] = true; return i; }
    for (i = 0; i < SIM7000_WL_MAX; i++) {
        if (!sim7000_wl_num[i][0]) {
            strncpy(sim7000_wl_num[i], number, sizeof(sim7000_wl_num[i]) - 1);
            sim7000_wl_num[i][sizeof(sim7000_wl_num[i]) - 1] = '\0';
            sim7000_wl_fixed[i] = fixed;
            return i;
        }
    }
    return -1;
}

// Whitelist set in code (e.g. the admin): always allowed, not removable by SMS.
void sim7000_allow_number(sim7000_t *dev, const char *number)
{
    (void)dev;
    if (sim7000_wl_add(number, true) < 0) log_error("sim7000", "whitelist full (%d numbers)", SIM7000_WL_MAX);
}

bool sim7000_sms_allowed(sim7000_t *dev)
{
    (void)dev;
    return sim7000_in_from[0] && sim7000_wl_find(sim7000_in_from) >= 0;
}

// Numbers added by SMS are kept in flash as gsm_wl0..gsm_wl5.
static void sim7000_wl_save(void)
{
    char key[12];
    for (int i = 0; i < SIM7000_WL_MAX; i++) {
        snprintf(key, sizeof(key), "gsm_wl%d", i);
        storage_save_str(key, sim7000_wl_fixed[i] ? "" : sim7000_wl_num[i]);
    }
    storage_flush();
}

// Load the SMS-added numbers saved in flash (call once in setup).
void sim7000_load_whitelist(sim7000_t *dev)
{
    (void)dev;
    char key[12], num[24];
    for (int i = 0; i < SIM7000_WL_MAX; i++) {
        snprintf(key, sizeof(key), "gsm_wl%d", i);
        storage_load_str(key, num, sizeof(num), "");
        if (num[0]) sim7000_wl_add(num, false);
    }
}

static bool sim7000_valid_number(const char *a)
{
    int digits = 0;
    for (int i = 0; a[i]; i++) {
        if (a[i] >= '0' && a[i] <= '9') digits++;
        else if (!(i == 0 && a[i] == '+')) return false;
    }
    return digits >= 9 && digits <= 15;
}

const char *sim7000_sms_arg(sim7000_t *dev);

// ALLOW <number>: 1 added, 0 already allowed, -1 bad number, -2 list full.
int32_t sim7000_whitelist_add_from_sms(sim7000_t *dev)
{
    const char *a = sim7000_sms_arg(dev);
    if (!sim7000_valid_number(a)) return -1;
    if (sim7000_wl_find(a) >= 0) return 0;
    if (sim7000_wl_add(a, false) < 0) return -2;
    sim7000_wl_save();
    log_info("sim7000", "whitelist: added %s", a);
    return 1;
}

// REMOVE <number>: 1 removed, 0 not on the list, -1 bad number, -2 fixed (admin/alert number).
int32_t sim7000_whitelist_remove_from_sms(sim7000_t *dev)
{
    const char *a = sim7000_sms_arg(dev);
    if (!sim7000_valid_number(a)) return -1;
    int i = sim7000_wl_find(a);
    if (i < 0) return 0;
    if (sim7000_wl_fixed[i]) return -2;
    sim7000_wl_num[i][0] = '\0';
    sim7000_wl_save();
    log_info("sim7000", "whitelist: removed %s", a);
    return 1;
}

// "Allowed: 0776162283 (fixed), 0775438588" — ready to send as an SMS.
const char *sim7000_whitelist_text(sim7000_t *dev)
{
    (void)dev;
    static char out[160];
    int n = snprintf(out, sizeof(out), "Allowed:");
    bool first = true;
    for (int i = 0; i < SIM7000_WL_MAX && n < (int)sizeof(out) - 1; i++) {
        if (!sim7000_wl_num[i][0]) continue;
        n += snprintf(out + n, sizeof(out) - n, "%s %s%s", first ? "" : ",", sim7000_wl_num[i], sim7000_wl_fixed[i] ? " (fixed)" : "");
        first = false;
    }
    return out;
}

// Read the oldest stored SMS into sms_from()/sms_text() and delete it from
// the SIM. Returns false when there is nothing to read.
bool sim7000_next_sms(sim7000_t *dev)
{
    static char buf[512];
    char resp[SIM7000_RESP_MAX];
    sim7000_in_from[0] = sim7000_in_text[0] = sim7000_in_cmd[0] = '\0';
    if (!sim7000_alive(dev)) return false;

    // Find the first index; the list can be long, the first entry is enough.
    sim7000_drain(dev);
    volt_uart_print(dev->uart_id, "AT+CMGL=\"ALL\",1\r");   // 1 = don't change status yet
    sim7000_wait_n(dev, "OK\r\n", 3000, buf, sizeof(buf));
    const char *p = strstr(buf, "+CMGL:");
    if (!p) return false;
    int idx = atoi(p + 6);

    // Read that one message: +CMGR: "<stat>","<from>","","<time>"\r\n<text>\r\n\r\nOK
    char cmd[24];
    snprintf(cmd, sizeof(cmd), "AT+CMGR=%d", idx);
    sim7000_drain(dev);
    volt_uart_print(dev->uart_id, cmd);
    volt_uart_print(dev->uart_id, "\r");
    if (!sim7000_wait_n(dev, "OK\r\n", 3000, buf, sizeof(buf)) || !(p = strstr(buf, "+CMGR:"))) return false;

    // sender = 2nd quoted field
    const char *q = strchr(p, '"');
    q = q ? strchr(q + 1, '"') : NULL;          // end of stat
    q = q ? strchr(q + 1, '"') : NULL;          // start of from
    const char *e = q ? strchr(q + 1, '"') : NULL;
    if (q && e) {
        int n = (int)(e - q - 1);
        if (n > (int)sizeof(sim7000_in_from) - 1) n = sizeof(sim7000_in_from) - 1;
        memcpy(sim7000_in_from, q + 1, n);
        sim7000_in_from[n] = '\0';
    }
    // text = the line after the header
    const char *t = strstr(p, "\r\n");
    if (t) {
        t += 2;
        const char *te = strstr(t, "\r\n\r\nOK");
        if (!te) te = strstr(t, "\r\nOK");
        int n = te ? (int)(te - t) : (int)strlen(t);
        if (n > (int)sizeof(sim7000_in_text) - 1) n = sizeof(sim7000_in_text) - 1;
        memcpy(sim7000_in_text, t, n);
        sim7000_in_text[n] = '\0';
    }
    // command = first word, upper-case
    int k = 0;
    const char *w = sim7000_in_text;
    while (*w == ' ') w++;
    while (*w && *w != ' ' && *w != '\r' && *w != '\n' && k < (int)sizeof(sim7000_in_cmd) - 1) {
        char ch = *w++;
        sim7000_in_cmd[k++] = (ch >= 'a' && ch <= 'z') ? (char)(ch - 32) : ch;
    }
    sim7000_in_cmd[k] = '\0';

    snprintf(cmd, sizeof(cmd), "AT+CMGD=%d", idx);
    sim7000_cmd(dev, cmd, "OK", 5000, resp);   // free the slot
    log_info("sim7000", "SMS from %s (%s): %s", sim7000_in_from,
             sim7000_sms_allowed(dev) ? "allowed" : "not allowed", sim7000_in_text);
    return true;
}

const char *sim7000_sms_from(sim7000_t *dev) { (void)dev; return sim7000_in_from; }
const char *sim7000_sms_text(sim7000_t *dev) { (void)dev; return sim7000_in_text; }

// Command word of the last SMS — only for whitelisted senders, else "".
const char *sim7000_sms_command(sim7000_t *dev)
{
    return sim7000_sms_allowed(dev) ? sim7000_in_cmd : "";
}

// true if the last SMS is from a whitelisted number and its command is `cmd`
// (case-insensitive).
bool sim7000_sms_command_is(sim7000_t *dev, const char *cmd)
{
    if (!sim7000_sms_allowed(dev)) return false;
    int i = 0;
    for (; cmd[i] && sim7000_in_cmd[i]; i++) {
        char a = cmd[i], b = sim7000_in_cmd[i];
        if (a >= 'a' && a <= 'z') a = (char)(a - 32);
        if (a != b) return false;
    }
    return cmd[i] == '\0' && sim7000_in_cmd[i] == '\0';
}

// First whole number after the command word of the last SMS ("SETID 7" → 7);
// -1 if there is none or the sender is not whitelisted.
int32_t sim7000_sms_arg_int(sim7000_t *dev)
{
    if (!sim7000_sms_allowed(dev)) return -1;
    const char *w = sim7000_in_text;
    while (*w == ' ') w++;
    while (*w && *w != ' ') w++;                 // skip the command word
    while (*w && (*w < '0' || *w > '9')) w++;    // to the first digit
    if (!*w) return -1;
    return (int32_t)atol(w);
}

// ---- SMS arguments + stored alert recipient ----------------------------------

static char sim7000_arg[24];

// Second word of the last SMS ("SETNUM 0771234567" → "0771234567"), only for
// whitelisted senders; "" otherwise.
const char *sim7000_sms_arg(sim7000_t *dev)
{
    sim7000_arg[0] = '\0';
    if (!sim7000_sms_allowed(dev)) return sim7000_arg;
    const char *w = sim7000_in_text;
    while (*w == ' ') w++;
    while (*w && *w != ' ') w++;      // skip the command word
    while (*w == ' ') w++;
    int n = 0;
    while (*w && *w != ' ' && *w != '\r' && *w != '\n' && n < (int)sizeof(sim7000_arg) - 1) sim7000_arg[n++] = *w++;
    sim7000_arg[n] = '\0';
    return sim7000_arg;
}

// true if the second word of the last (whitelisted) SMS equals `word`, case-insensitive.
bool sim7000_sms_arg_is(sim7000_t *dev, const char *word)
{
    const char *a = sim7000_sms_arg(dev);
    int i = 0;
    for (; word[i] && a[i]; i++) {
        char x = word[i], y = a[i];
        if (x >= 'a' && x <= 'z') x = (char)(x - 32);
        if (y >= 'a' && y <= 'z') y = (char)(y - 32);
        if (x != y) return false;
    }
    return word[i] == '\0' && a[i] == '\0';
}

#define SIM7000_ALERT_KEY "gsm_alert_to"
static char sim7000_alert_to[24];

// Load the alert recipient saved by set_alert_number_from_sms(), or use
// `fallback` if none was ever set. The recipient is also allowed to send commands.
void sim7000_load_alert_number(sim7000_t *dev, const char *fallback)
{
    storage_load_str(SIM7000_ALERT_KEY, sim7000_alert_to, sizeof(sim7000_alert_to), fallback);
    if (!sim7000_alert_to[0]) {
        strncpy(sim7000_alert_to, fallback, sizeof(sim7000_alert_to) - 1);
        sim7000_alert_to[sizeof(sim7000_alert_to) - 1] = '\0';
    }
    sim7000_allow_number(dev, sim7000_alert_to);
    log_info("sim7000", "alert SMS go to %s", sim7000_alert_to);
}

const char *sim7000_alert_number(sim7000_t *dev)
{
    (void)dev;
    return sim7000_alert_to;
}

// Take the number from the last whitelisted SMS ("SETNUM 0771234567"),
// check it looks like a phone number, save it to flash and allow it.
bool sim7000_set_alert_number_from_sms(sim7000_t *dev)
{
    const char *a = sim7000_sms_arg(dev);
    int digits = 0;
    for (int i = 0; a[i]; i++) {
        if (a[i] >= '0' && a[i] <= '9') digits++;
        else if (!(i == 0 && a[i] == '+')) return false;
    }
    if (digits < 9 || digits > 15) return false;
    strncpy(sim7000_alert_to, a, sizeof(sim7000_alert_to) - 1);
    sim7000_alert_to[sizeof(sim7000_alert_to) - 1] = '\0';
    storage_save_str(SIM7000_ALERT_KEY, sim7000_alert_to);
    storage_flush();
    sim7000_allow_number(dev, sim7000_alert_to);
    log_info("sim7000", "alert number set to %s", sim7000_alert_to);
    return true;
}
