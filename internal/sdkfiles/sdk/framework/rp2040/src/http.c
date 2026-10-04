// HTTP stubs for RP2040 — requires WiFi, which isn't supported on standard RP2040.
// These stubs allow programs to compile; they log a warning and return errors at runtime.

#include "volt/http.h"
#include "volt/log.h"

volt_error_t http_get(const char *url, char *buf, size_t buf_size, int *status_code)
{
    (void)url;
    if (buf && buf_size > 0) buf[0] = '\0';
    if (status_code) *status_code = 0;
    log_warn("http", "HTTP not supported on RP2040 (no WiFi)");
    return VOLT_ERR_NOT_SUPPORTED;
}

volt_error_t http_post(const char *url, const char *body, const char *content_type,
                        char *response, size_t response_size, int *status_code)
{
    (void)url; (void)body; (void)content_type;
    if (response && response_size > 0) response[0] = '\0';
    if (status_code) *status_code = 0;
    log_warn("http", "HTTP not supported on RP2040 (no WiFi)");
    return VOLT_ERR_NOT_SUPPORTED;
}
