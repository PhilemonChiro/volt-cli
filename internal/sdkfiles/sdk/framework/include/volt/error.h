#ifndef VOLT_ERROR_H
#define VOLT_ERROR_H

// Every Volt framework function that can fail returns this type.
// VOLT_OK (0) means success. Negative values are errors.
// Check with: if (result != VOLT_OK) { handle_error(); }
typedef enum {
    VOLT_OK                     =  0,

    // General
    VOLT_ERR_INVALID_ARG        = -1,   // NULL pointer or invalid parameter
    VOLT_ERR_TIMEOUT            = -2,
    VOLT_ERR_NO_MEMORY          = -3,
    VOLT_ERR_NOT_SUPPORTED      = -4,
    VOLT_ERR_INTERNAL           = -5,   // ESP-IDF call failed unexpectedly

    // Pin errors (used by pin.h)
    VOLT_ERR_INVALID_PIN        = -10,
    VOLT_ERR_PIN_IN_USE         = -11,
    VOLT_ERR_WRONG_PIN_MODE     = -12,
    VOLT_ERR_PIN_CONFIG_FAILED  = -13,  // ESP-IDF gpio_config returned error

    // Task errors (used by task.h)
    VOLT_ERR_TOO_MANY_TASKS     = -20,
    VOLT_ERR_TASK_CREATE_FAILED = -21,  // xTaskCreate returned errCOULD_NOT_ALLOCATE
    VOLT_ERR_TIMER_FAILED       = -22,  // hardware timer creation failed

    // WiFi errors (used by wifi.h)
    VOLT_ERR_WIFI_INIT_FAILED   = -30,  // esp_wifi_init failed
    VOLT_ERR_WIFI_START_FAILED  = -31,  // esp_wifi_start failed
    VOLT_ERR_WIFI_NOT_CONNECTED = -32,

    // MQTT errors (used by mqtt.h)
    VOLT_ERR_MQTT_INIT_FAILED   = -40,  // esp_mqtt_client_init returned NULL
    VOLT_ERR_MQTT_START_FAILED  = -41,  // esp_mqtt_client_start failed
    VOLT_ERR_MQTT_NOT_CONNECTED = -42,
    VOLT_ERR_MQTT_PUBLISH_FAILED = -43,
    VOLT_ERR_MQTT_SUB_FULL      = -44,  // max subscriptions reached

    // HTTP errors (used by http.h)
    VOLT_ERR_HTTP_INIT_FAILED   = -50,
    VOLT_ERR_HTTP_FAILED        = -51,

    // Storage errors (used by storage.h)
    VOLT_ERR_KEY_NOT_FOUND      = -60,
    VOLT_ERR_STORAGE_FULL       = -61,
    VOLT_ERR_STORAGE_INIT_FAILED = -62,
    VOLT_ERR_STORAGE_WRITE_FAILED = -63,
} volt_error_t;

// Returns the error name as a string, for logging.
const char *volt_error_name(volt_error_t error);

#endif // VOLT_ERROR_H
