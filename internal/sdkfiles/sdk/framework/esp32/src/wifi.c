#include "volt/wifi.h"
#include "volt/log.h"
#include "volt/task.h"

#include <string.h>
#include "esp_wifi.h"
#include "esp_event.h"
#include "nvs_flash.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_timer.h"
// ESP8266 RTOS SDK uses the legacy tcpip_adapter; esp_netif is ESP-IDF v4+.
#ifdef CONFIG_IDF_TARGET_ESP8266
#include "tcpip_adapter.h"
#include "lwip/inet.h"
#else
#include "esp_netif.h"
#endif

// One-shot timer for non-blocking reconnect with backoff
static esp_timer_handle_t s_reconnect_timer = NULL;

static void reconnect_timer_callback(void *arg)
{
    esp_wifi_connect();
}

// Internal state
// volatile: written from event task, read from main task (dual-core ESP32)
static volatile bool s_connected = false;
static volatile bool s_auto_reconnect = true;
static bool s_initialized = false;
static char s_ip_str[16] = "0.0.0.0";
static char s_ssid[33] = {0};
static char s_password[65] = {0};
static int  s_retry_count = 0;
#define MAX_RETRY_DELAY_MS 30000

// User callbacks
static wifi_callback_t s_on_connect = NULL;
static wifi_callback_t s_on_disconnect = NULL;

// Connection statistics
static wifi_stats_t s_stats = {0};
static uint32_t s_current_session_start = 0;

// Handles all WiFi and IP events
static void wifi_event_handler(void *arg, esp_event_base_t base,
                                int32_t id, void *data)
{
    if (base == WIFI_EVENT) {
        if (id == WIFI_EVENT_STA_START) {
            log_info("wifi", "connecting to '%s'...", s_ssid);
            esp_wifi_connect();

        } else if (id == WIFI_EVENT_STA_DISCONNECTED) {
            // Track session duration
            if (s_connected && s_current_session_start > 0) {
                uint32_t session = volt_tick_ms() - s_current_session_start;
                s_stats.uptime_ms += session;
                if (session > s_stats.longest_uptime_ms) {
                    s_stats.longest_uptime_ms = session;
                }
            }

            s_connected = false;
            s_stats.disconnects++;

            // Only auto-reconnect if not manually disconnected
            if (!s_auto_reconnect) {
                log_info("wifi", "disconnected (manual — not reconnecting)");
                return;
            }

            s_retry_count++;

            // Exponential backoff: 1s, 2s, 4s, 8s, 16s, 30s max
            int shift = s_retry_count > 5 ? 5 : (s_retry_count > 0 ? s_retry_count - 1 : 0);
            int delay_ms = 1000 * (1 << shift);
            if (delay_ms > MAX_RETRY_DELAY_MS) delay_ms = MAX_RETRY_DELAY_MS;

            log_warn("wifi", "disconnected — retry #%d in %dms (total: %lu)",
                     s_retry_count, delay_ms, (unsigned long)s_stats.disconnects);

            if (s_on_disconnect) s_on_disconnect();

            // Non-blocking reconnect: schedule a one-shot timer with backoff delay.
            // NEVER block the event handler — it would freeze ALL event processing.
            if (s_reconnect_timer == NULL) {
                esp_timer_create_args_t timer_args = {
                    .callback = reconnect_timer_callback,
                    .name = "wifi_reconnect",
                };
                esp_timer_create(&timer_args, &s_reconnect_timer);
            }
            // Stop any pending timer, start new one with backoff delay
            esp_timer_stop(s_reconnect_timer);
            esp_timer_start_once(s_reconnect_timer, (uint64_t)delay_ms * 1000);
        }

    } else if (base == IP_EVENT) {
        if (id == IP_EVENT_STA_GOT_IP) {
            ip_event_got_ip_t *event = (ip_event_got_ip_t *)data;
// esp_ip4addr_ntoa() is ESP-IDF only. ESP8266 RTOS SDK uses lwip's ip4addr_ntoa().
#ifdef CONFIG_IDF_TARGET_ESP8266
            strncpy(s_ip_str, ip4addr_ntoa((const ip4_addr_t *)&event->ip_info.ip),
                    sizeof(s_ip_str) - 1);
            s_ip_str[sizeof(s_ip_str) - 1] = '\0';
#else
            esp_ip4addr_ntoa(&event->ip_info.ip, s_ip_str, sizeof(s_ip_str));
#endif

            s_connected = true;
            s_retry_count = 0;
            s_current_session_start = volt_tick_ms();
            s_stats.connected_at = s_current_session_start;

            log_info("wifi", "connected — ip: %s", s_ip_str);

            if (s_on_connect) s_on_connect();
        }
    }
}

volt_error_t wifi_connect(const char *ssid, const char *password)
{
    if (ssid == NULL) return VOLT_ERR_INVALID_ARG;

    strncpy(s_ssid, ssid, sizeof(s_ssid) - 1);
    s_ssid[sizeof(s_ssid) - 1] = '\0';
    if (password) {
        strncpy(s_password, password, sizeof(s_password) - 1);
        s_password[sizeof(s_password) - 1] = '\0';
    } else {
        s_password[0] = '\0';
    }

    if (!s_initialized) {
        // Ensure NVS is initialized — WiFi needs it for calibration data
        nvs_flash_init();

// ESP8266 RTOS SDK uses tcpip_adapter_init() instead of esp_netif_init(),
// and has no esp_netif_create_default_wifi_sta() — the adapter is always present.
#ifdef CONFIG_IDF_TARGET_ESP8266
        tcpip_adapter_init();
        esp_event_loop_create_default();
#else
        esp_netif_init();
        esp_event_loop_create_default();
        esp_netif_create_default_wifi_sta();
#endif

        wifi_init_config_t cfg = WIFI_INIT_CONFIG_DEFAULT();
        esp_err_t err = esp_wifi_init(&cfg);
        if (err != ESP_OK) {
            log_error("wifi", "init failed (esp_err=%d)", err);
            return VOLT_ERR_WIFI_INIT_FAILED;
        }

#ifdef CONFIG_IDF_TARGET_ESP8266
        // ESP8266 RTOS SDK uses the legacy event loop
        esp_event_handler_register(WIFI_EVENT, ESP_EVENT_ANY_ID, wifi_event_handler, NULL);
        esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP, wifi_event_handler, NULL);
#else
        esp_event_handler_instance_register(WIFI_EVENT, ESP_EVENT_ANY_ID,
                                            wifi_event_handler, NULL, NULL);
        esp_event_handler_instance_register(IP_EVENT, IP_EVENT_STA_GOT_IP,
                                            wifi_event_handler, NULL, NULL);
#endif

        s_initialized = true;
    }

    wifi_config_t wifi_config = {0};
    strncpy((char *)wifi_config.sta.ssid, s_ssid, sizeof(wifi_config.sta.ssid) - 1);
    wifi_config.sta.ssid[sizeof(wifi_config.sta.ssid) - 1] = '\0';
    strncpy((char *)wifi_config.sta.password, s_password, sizeof(wifi_config.sta.password) - 1);
    wifi_config.sta.password[sizeof(wifi_config.sta.password) - 1] = '\0';
    wifi_config.sta.threshold.authmode = password ? WIFI_AUTH_WPA2_PSK : WIFI_AUTH_OPEN;

    esp_wifi_set_mode(WIFI_MODE_STA);
    esp_wifi_set_config(WIFI_IF_STA, &wifi_config);

    esp_err_t err = esp_wifi_start();
    if (err != ESP_OK) {
        log_error("wifi", "start failed (esp_err=%d)", err);
        return VOLT_ERR_WIFI_START_FAILED;
    }

    s_retry_count = 0;
    s_auto_reconnect = true;
    return VOLT_OK;
}

volt_error_t wifi_disconnect(void)
{
    // Set flag BEFORE disconnecting — the event handler checks this
    s_auto_reconnect = false;

    // Cancel any pending reconnect timer so it doesn't fire after disconnect
    if (s_reconnect_timer) {
        esp_timer_stop(s_reconnect_timer);
    }

    esp_wifi_disconnect();
    s_connected = false;
    log_info("wifi", "disconnected (manual)");
    return VOLT_OK;
}

bool wifi_is_connected(void)
{
    return s_connected;
}

const char *wifi_get_ip(void)
{
    return s_ip_str;
}

int wifi_get_rssi(void)
{
    if (!s_connected) return 0;

    wifi_ap_record_t ap_info;
    if (esp_wifi_sta_get_ap_info(&ap_info) == ESP_OK) {
        return ap_info.rssi;
    }
    return 0;
}

void wifi_on_connect(wifi_callback_t callback)
{
    s_on_connect = callback;
}

void wifi_on_disconnect(wifi_callback_t callback)
{
    s_on_disconnect = callback;
}

wifi_stats_t wifi_get_stats(void)
{
    wifi_stats_t stats = s_stats;
    if (s_connected && s_current_session_start > 0) {
        uint32_t current_session = volt_tick_ms() - s_current_session_start;
        stats.uptime_ms += current_session;
        if (current_session > stats.longest_uptime_ms) {
            stats.longest_uptime_ms = current_session;
        }
    }
    stats.avg_rssi = wifi_get_rssi();
    return stats;
}
