// MQTT stubs for RP2040 — requires WiFi, which isn't supported on standard RP2040.
// These stubs allow programs to compile; they log a warning and return errors at runtime.

#include "volt/mqtt.h"
#include "volt/log.h"

volt_error_t mqtt_connect(const char *broker_url)
{
    (void)broker_url;
    log_warn("mqtt", "MQTT not supported on RP2040 (no WiFi)");
    return VOLT_ERR_NOT_SUPPORTED;
}

volt_error_t mqtt_connect_auth(const char *broker_url, const char *user, const char *pass)
{
    (void)broker_url; (void)user; (void)pass;
    log_warn("mqtt", "MQTT not supported on RP2040 (no WiFi)");
    return VOLT_ERR_NOT_SUPPORTED;
}

volt_error_t mqtt_disconnect(void)
{
    return VOLT_OK;
}

bool mqtt_is_connected(void)
{
    return false;
}

volt_error_t mqtt_send(const char *topic, const char *data)
{
    (void)topic; (void)data;
    return VOLT_ERR_NOT_SUPPORTED;
}

volt_error_t mqtt_send_float(const char *topic, float value)
{
    (void)topic; (void)value;
    return VOLT_ERR_NOT_SUPPORTED;
}

volt_error_t mqtt_send_int(const char *topic, int value)
{
    (void)topic; (void)value;
    return VOLT_ERR_NOT_SUPPORTED;
}

volt_error_t mqtt_on_receive(const char *topic, mqtt_handler_t handler)
{
    (void)topic; (void)handler;
    return VOLT_ERR_NOT_SUPPORTED;
}
