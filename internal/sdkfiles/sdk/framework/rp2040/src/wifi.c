// WiFi stubs for RP2040 — no WiFi on standard RP2040.
// Pico W has WiFi via CYW43439 but that requires a separate driver stack.
// These stubs allow programs to compile and link; they log a warning at runtime.

#include "volt/wifi.h"
#include "volt/log.h"

volt_error_t wifi_connect(const char *ssid, const char *password)
{
    (void)ssid; (void)password;
    log_warn("wifi", "WiFi not supported on RP2040");
    return VOLT_ERR_NOT_SUPPORTED;
}

volt_error_t wifi_disconnect(void)
{
    return VOLT_OK;
}

bool wifi_is_connected(void)
{
    return false;
}

const char *wifi_get_ip(void)
{
    return "0.0.0.0";
}

int wifi_get_rssi(void)
{
    return 0;
}

void wifi_on_connect(wifi_callback_t callback)
{
    (void)callback;
    log_warn("wifi", "WiFi callbacks not supported on RP2040");
}

void wifi_on_disconnect(wifi_callback_t callback)
{
    (void)callback;
    log_warn("wifi", "WiFi callbacks not supported on RP2040");
}

wifi_stats_t wifi_get_stats(void)
{
    wifi_stats_t stats = {0};
    return stats;
}
