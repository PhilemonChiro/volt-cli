/* SIM800L GSM/GPRS modem — AT command driver over UART */

#include "volt/sensor.h"
#include "volt/uart.h"
#include "volt/task.h"
#include "volt/log.h"
#include <string.h>
#include <stdio.h>

#define SIM800L_RESP_MAX 160

// Send a command and collect the reply until `expect` appears or timeout.
// Returns true if `expect` was seen. The reply (NUL-terminated) is left in resp.
static bool sim800l_cmd(sim800l_t *dev, const char *cmd, const char *expect,
                        uint32_t timeout_ms, char *resp)
{
    // volt_uart_flush only waits for TX; drain unsolicited bytes ("SMS Ready",
    // "+CREG: ...") so a stale "OK" can't satisfy this command.
    while (volt_uart_read_byte(dev->uart_id) >= 0) {}
    volt_uart_print(dev->uart_id, cmd);
    volt_uart_print(dev->uart_id, "\r\n");

    int len = 0;
    resp[0] = '\0';
    uint32_t t0 = volt_tick_ms();
    while (volt_tick_ms() - t0 < timeout_ms) {
        int c = volt_uart_read_byte(dev->uart_id);
        if (c < 0) { volt_delay_ms(5); continue; }
        if (len < SIM800L_RESP_MAX - 1) { resp[len++] = (char)c; resp[len] = '\0'; }
        if (strstr(resp, expect)) return true;
        if (strstr(resp, "ERROR")) return false;
    }
    return false;
}

volt_error_t sim800l_init(sim800l_t *dev, uint8_t uart_id)
{
    dev->uart_id = uart_id;
    char resp[SIM800L_RESP_MAX];
    bool ok = false;
    for (int i = 0; i < 5 && !ok; i++) {
        ok = sim800l_cmd(dev, "AT", "OK", 1000, resp);
    }
    if (!ok) {
        log_error("sim800l", "no response on uart %d (check 4.0V supply, GND, RX/TX)", uart_id);
        return VOLT_ERR_INTERNAL;
    }
    sim800l_cmd(dev, "ATE0", "OK", 1000, resp);       // echo off
    sim800l_cmd(dev, "AT+CMGF=1", "OK", 1000, resp);  // SMS text mode
    log_info("sim800l", "modem ready on uart %d", uart_id);
    return VOLT_OK;
}

bool sim800l_ready(sim800l_t *dev)
{
    char resp[SIM800L_RESP_MAX];
    return sim800l_cmd(dev, "AT", "OK", 1000, resp);
}

bool sim800l_registered(sim800l_t *dev)
{
    char resp[SIM800L_RESP_MAX];
    if (!sim800l_cmd(dev, "AT+CREG?", "OK", 1000, resp)) return false;
    // +CREG: <n>,<stat>  — stat 1 = home, 5 = roaming
    const char *p = strstr(resp, "+CREG:");
    if (!p) return false;
    p = strchr(p, ',');
    if (!p) return false;
    int stat = p[1] - '0';
    return stat == 1 || stat == 5;
}

int32_t sim800l_signal(sim800l_t *dev)
{
    char resp[SIM800L_RESP_MAX];
    if (!sim800l_cmd(dev, "AT+CSQ", "OK", 1000, resp)) return 99;
    const char *p = strstr(resp, "+CSQ:");
    if (!p) return 99;
    int rssi = 99;
    sscanf(p, "+CSQ: %d", &rssi);
    return rssi;
}

bool sim800l_send_sms(sim800l_t *dev, const char *number, const char *text)
{
    char resp[SIM800L_RESP_MAX];
    char cmd[40];
    snprintf(cmd, sizeof(cmd), "AT+CMGS=\"%s\"", number);
    if (!sim800l_cmd(dev, cmd, ">", 3000, resp)) {
        log_error("sim800l", "no SMS prompt");
        return false;
    }
    volt_uart_print(dev->uart_id, text);
    static const uint8_t ctrl_z = 0x1A;
    volt_uart_write(dev->uart_id, &ctrl_z, 1);
    // network round-trip; +CMGS: <ref> then OK
    bool ok = sim800l_cmd(dev, "", "+CMGS", 10000, resp);
    if (!ok) log_error("sim800l", "SMS send failed: %s", resp);
    else     log_info("sim800l", "SMS sent to %s", number);
    return ok;
}
