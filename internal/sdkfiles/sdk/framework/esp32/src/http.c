#include "volt/http.h"
#include "volt/log.h"

#include <string.h>
#include "esp_http_client.h"
// esp_crt_bundle.h is only available on ESP-IDF v4+ (ESP32/ESP32-S2/etc.)
// ESP8266 RTOS SDK does not include it — skip TLS certificate bundle there.
#ifndef CONFIG_IDF_TARGET_ESP8266
#include "esp_crt_bundle.h"
#endif

// Shared state for the response handler callback
typedef struct {
    char *buf;
    size_t buf_size;
    size_t received;
} http_response_t;

// Callback: ESP-IDF calls this as response data arrives
static esp_err_t http_event_handler(esp_http_client_event_t *evt)
{
    http_response_t *resp = (http_response_t *)evt->user_data;

    if (evt->event_id == HTTP_EVENT_ON_DATA && resp && resp->buf) {
        if (resp->buf_size == 0) return ESP_OK;
        if (resp->received >= resp->buf_size - 1) return ESP_OK;
        // Copy received data into our buffer (append)
        size_t space = resp->buf_size - resp->received - 1; // leave room for null
        size_t copy = evt->data_len < space ? evt->data_len : space;
        if (copy > 0) {
            memcpy(resp->buf + resp->received, evt->data, copy);
            resp->received += copy;
            resp->buf[resp->received] = '\0';
        }
    }

    return ESP_OK;
}

volt_error_t http_get(const char *url, char *buf, size_t buf_size, int *status_code)
{
    if (url == NULL) return VOLT_ERR_INVALID_ARG;

    // Zero the response buffer
    http_response_t resp = {
        .buf = buf,
        .buf_size = buf_size,
        .received = 0,
    };
    if (buf && buf_size > 0) buf[0] = '\0';

    esp_http_client_config_t config = {
        .url = url,
        .event_handler = http_event_handler,
        .user_data = &resp,
        .timeout_ms = 10000,
// TLS certificate validation via ESP-IDF's built-in certificate bundle.
// This validates server certificates for HTTPS, preventing MITM attacks.
// Not available on ESP8266 RTOS SDK — HTTPS works but without bundle validation.
#ifndef CONFIG_IDF_TARGET_ESP8266
        .crt_bundle_attach = esp_crt_bundle_attach,
#endif
    };

    esp_http_client_handle_t client = esp_http_client_init(&config);
    if (client == NULL) {
        log_error("http", "client init failed for %s", url);
        return VOLT_ERR_HTTP_INIT_FAILED;
    }

    esp_err_t err = esp_http_client_perform(client);

    volt_error_t result = VOLT_OK;
    if (err == ESP_OK) {
        int code = esp_http_client_get_status_code(client);
        if (status_code) *status_code = code;
        log_debug("http", "GET %s → %d (%d bytes)", url, code, (int)resp.received);
    } else {
        log_error("http", "GET %s failed (esp_err=%d)", url, err);
        result = (err == ESP_ERR_HTTP_CONNECT) ? VOLT_ERR_TIMEOUT : VOLT_ERR_HTTP_FAILED;
    }

    esp_http_client_cleanup(client);
    return result;
}

volt_error_t http_post(const char *url, const char *body, const char *content_type,
                        char *response, size_t response_size, int *status_code)
{
    if (url == NULL) return VOLT_ERR_INVALID_ARG;

    http_response_t resp = {
        .buf = response,
        .buf_size = response_size,
        .received = 0,
    };
    if (response && response_size > 0) response[0] = '\0';

    esp_http_client_config_t config = {
        .url = url,
        .event_handler = http_event_handler,
        .user_data = &resp,
        .timeout_ms = 10000,
        .method = HTTP_METHOD_POST,
#ifndef CONFIG_IDF_TARGET_ESP8266
        .crt_bundle_attach = esp_crt_bundle_attach,
#endif
    };

    esp_http_client_handle_t client = esp_http_client_init(&config);
    if (client == NULL) {
        log_error("http", "client init failed for %s", url);
        return VOLT_ERR_HTTP_INIT_FAILED;
    }

    // Set content type (default to JSON)
    esp_http_client_set_header(client, "Content-Type",
                                content_type ? content_type : "application/json");

    // Set body
    if (body) {
        esp_http_client_set_post_field(client, body, strlen(body));
    }

    esp_err_t err = esp_http_client_perform(client);

    volt_error_t result = VOLT_OK;
    if (err == ESP_OK) {
        int code = esp_http_client_get_status_code(client);
        if (status_code) *status_code = code;
        log_debug("http", "POST %s → %d (%d bytes)", url, code, (int)resp.received);
    } else {
        log_error("http", "POST %s failed (esp_err=%d)", url, err);
        result = (err == ESP_ERR_HTTP_CONNECT) ? VOLT_ERR_TIMEOUT : VOLT_ERR_HTTP_FAILED;
    }

    esp_http_client_cleanup(client);
    return result;
}
