#include "volt/mqtt.h"
#include "volt/log.h"

#include <stdio.h>
#include <string.h>
#include "mqtt_client.h"
// esp_mac.h is ESP-IDF v5+ only. On ESP8266 RTOS SDK, esp_read_mac() is
// declared in esp_wifi.h which is pulled in transitively — no extra header needed.
#ifndef CONFIG_IDF_TARGET_ESP8266
#include "esp_mac.h"
#endif

// Internal state — volatile because written from event task, read from main task
static esp_mqtt_client_handle_t s_client = NULL;
static volatile bool s_connected = false;

// Subscription table — up to 16 topic/handler pairs
#define MAX_SUBSCRIPTIONS 16

typedef struct {
    char topic[64];
    mqtt_handler_t handler;
} subscription_t;

static subscription_t s_subs[MAX_SUBSCRIPTIONS];
static int s_sub_count = 0;

// Auto-generated client ID from MAC address: "volt-AABBCC"
static char s_client_id[16] = {0};

static void generate_client_id(void)
{
    uint8_t mac[6];
    esp_read_mac(mac, ESP_MAC_WIFI_STA);
    snprintf(s_client_id, sizeof(s_client_id), "volt-%02X%02X%02X",
             mac[3], mac[4], mac[5]);
}

// Find the handler for a topic by matching against subscriptions.
static mqtt_handler_t find_handler(const char *topic)
{
    for (int i = 0; i < s_sub_count; i++) {
        if (strcmp(s_subs[i].topic, topic) == 0) {
            return s_subs[i].handler;
        }
        int len = strlen(s_subs[i].topic);
        if (len > 0 && s_subs[i].topic[len - 1] == '#') {
            if (strncmp(s_subs[i].topic, topic, len - 1) == 0) {
                return s_subs[i].handler;
            }
        }
    }
    return NULL;
}

// Handles all MQTT events from ESP-IDF
static void mqtt_event_handler(void *arg, esp_event_base_t base,
                                int32_t event_id, void *event_data)
{
    esp_mqtt_event_handle_t event = (esp_mqtt_event_handle_t)event_data;

    switch (event_id) {
        case MQTT_EVENT_CONNECTED:
            s_connected = true;
            log_info("mqtt", "connected to broker (id: %s)", s_client_id);
            for (int i = 0; i < s_sub_count; i++) {
                esp_mqtt_client_subscribe(s_client, s_subs[i].topic, 0);
                log_debug("mqtt", "subscribed: %s", s_subs[i].topic);
            }
            break;

        case MQTT_EVENT_DISCONNECTED:
            s_connected = false;
            log_warn("mqtt", "disconnected — will auto-reconnect");
            break;

        case MQTT_EVENT_DATA: {
            // Validate lengths — defense against malformed packets
            if (event->topic_len <= 0 || event->data_len < 0) {
                log_warn("mqtt", "received malformed message (topic_len=%d data_len=%d)",
                         event->topic_len, event->data_len);
                break;
            }
            if (event->topic == NULL || event->data == NULL) {
                break;
            }

            char topic_buf[128];
            char data_buf[256];

            int topic_len = event->topic_len < 127 ? event->topic_len : 127;
            int data_len = event->data_len < 255 ? event->data_len : 255;

            memcpy(topic_buf, event->topic, topic_len);
            topic_buf[topic_len] = '\0';

            memcpy(data_buf, event->data, data_len);
            data_buf[data_len] = '\0';

            log_debug("mqtt", "received: %s = %s", topic_buf, data_buf);

            mqtt_handler_t handler = find_handler(topic_buf);
            if (handler) {
                handler(topic_buf, data_buf, data_len);
            }
            break;
        }

        case MQTT_EVENT_ERROR:
            log_error("mqtt", "error (type=%d)", event->error_handle->error_type);
            break;

        default:
            break;
    }
}

// Internal: create and start MQTT client with config
static volt_error_t _mqtt_start(const esp_mqtt_client_config_t *cfg)
{
    s_client = esp_mqtt_client_init(cfg);
    if (s_client == NULL) {
        log_error("mqtt", "client init failed");
        return VOLT_ERR_MQTT_INIT_FAILED;
    }

    esp_mqtt_client_register_event(s_client, ESP_EVENT_ANY_ID,
                                    mqtt_event_handler, NULL);

    esp_err_t err = esp_mqtt_client_start(s_client);
    if (err != ESP_OK) {
        log_error("mqtt", "start failed (esp_err=%d)", err);
        return VOLT_ERR_MQTT_START_FAILED;
    }

    return VOLT_OK;
}

volt_error_t mqtt_connect(const char *broker_url)
{
    if (broker_url == NULL) return VOLT_ERR_INVALID_ARG;

    generate_client_id();

#ifdef CONFIG_IDF_TARGET_ESP8266
    // ESP8266 RTOS SDK (ESP-IDF v3.x) uses flat config struct
    esp_mqtt_client_config_t cfg = {
        .uri = broker_url,
        .client_id = s_client_id,
    };
#else
    esp_mqtt_client_config_t cfg = {
        .broker.address.uri = broker_url,
        .credentials.client_id = s_client_id,
    };
#endif

    log_info("mqtt", "connecting to %s (id: %s)", broker_url, s_client_id);
    return _mqtt_start(&cfg);
}

volt_error_t mqtt_connect_auth(const char *broker_url, const char *username,
                                const char *password)
{
    if (broker_url == NULL) return VOLT_ERR_INVALID_ARG;

    generate_client_id();

#ifdef CONFIG_IDF_TARGET_ESP8266
    esp_mqtt_client_config_t cfg = {
        .uri = broker_url,
        .client_id = s_client_id,
        .username = username,
        .password = password,
    };
#else
    esp_mqtt_client_config_t cfg = {
        .broker.address.uri = broker_url,
        .credentials.client_id = s_client_id,
        .credentials.username = username,
        .credentials.authentication.password = password,
    };
#endif

    log_info("mqtt", "connecting to %s with auth (user: %s, id: %s)",
             broker_url, username ? username : "(none)", s_client_id);
    return _mqtt_start(&cfg);
}

volt_error_t mqtt_disconnect(void)
{
    if (s_client == NULL) return VOLT_OK;

    esp_mqtt_client_stop(s_client);
    esp_mqtt_client_destroy(s_client);
    s_client = NULL;
    s_connected = false;
    log_info("mqtt", "disconnected (manual)");
    return VOLT_OK;
}

bool mqtt_is_connected(void)
{
    return s_connected;
}

volt_error_t mqtt_send(const char *topic, const char *data)
{
    if (!s_connected) return VOLT_ERR_MQTT_NOT_CONNECTED;

    int msg_id = esp_mqtt_client_publish(s_client, topic, data,
                                          strlen(data), 0, 0);
    if (msg_id < 0) {
        log_error("mqtt", "publish failed: %s", topic);
        return VOLT_ERR_MQTT_PUBLISH_FAILED;
    }

    return VOLT_OK;
}

volt_error_t mqtt_send_float(const char *topic, float value)
{
    char buf[16];
    snprintf(buf, sizeof(buf), "%.2f", value);
    return mqtt_send(topic, buf);
}

volt_error_t mqtt_send_int(const char *topic, int value)
{
    char buf[12];
    snprintf(buf, sizeof(buf), "%d", value);
    return mqtt_send(topic, buf);
}

volt_error_t mqtt_on_receive(const char *topic, mqtt_handler_t handler)
{
    if (topic == NULL || handler == NULL) return VOLT_ERR_INVALID_ARG;

    if (s_sub_count >= MAX_SUBSCRIPTIONS) {
        log_error("mqtt", "max subscriptions reached (%d)", MAX_SUBSCRIPTIONS);
        return VOLT_ERR_MQTT_SUB_FULL;
    }

    strncpy(s_subs[s_sub_count].topic, topic, sizeof(s_subs[s_sub_count].topic) - 1);
    s_subs[s_sub_count].topic[sizeof(s_subs[s_sub_count].topic) - 1] = '\0';
    s_subs[s_sub_count].handler = handler;
    s_sub_count++;

    if (s_connected && s_client) {
        esp_mqtt_client_subscribe(s_client, topic, 0);
        log_debug("mqtt", "subscribed: %s", topic);
    }

    return VOLT_OK;
}
