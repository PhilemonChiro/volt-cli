#ifndef VOLT_MQTT_H
#define VOLT_MQTT_H

#include <stdbool.h>
#include "volt/error.h"

// Connect to an MQTT broker. Non-blocking — connects in background.
// URL format: "mqtt://host" or "mqtt://user:pass@host" or "mqtts://host"
volt_error_t mqtt_connect(const char *broker_url);

// Connect with explicit username and password (for brokers requiring auth).
volt_error_t mqtt_connect_auth(const char *broker_url, const char *username,
                                const char *password);

// Disconnect from broker.
volt_error_t mqtt_disconnect(void);

// Check if connected to broker.
bool mqtt_is_connected(void);

// Publish a string to a topic.
volt_error_t mqtt_send(const char *topic, const char *data);

// Publish a float to a topic (auto-converts to string, 2 decimal places).
volt_error_t mqtt_send_float(const char *topic, float value);

// Publish an int to a topic (auto-converts to string).
volt_error_t mqtt_send_int(const char *topic, int value);

// Subscribe to a topic and register a handler.
// Handler is called whenever a message arrives on that topic.
// Supports up to 8 subscriptions.
typedef void (*mqtt_handler_t)(const char *topic, const char *data, int data_len);
volt_error_t mqtt_on_receive(const char *topic, mqtt_handler_t handler);

#endif // VOLT_MQTT_H
