#ifndef VOLT_WIFI_H
#define VOLT_WIFI_H

#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// Connect to WiFi. Non-blocking — starts connection in background.
// Auto-reconnects on disconnect. Call wifi_is_connected() to check status.
volt_error_t wifi_connect(const char *ssid, const char *password);

// Disconnect and stop auto-reconnect.
volt_error_t wifi_disconnect(void);

// Returns true if connected and has an IP address.
bool wifi_is_connected(void);

// Get the IP address as a string. Returns "0.0.0.0" if not connected.
const char *wifi_get_ip(void);

// Get signal strength in dBm. Returns 0 if not connected.
int wifi_get_rssi(void);

// Register a callback for when WiFi connects (got IP address).
typedef void (*wifi_callback_t)(void);
void wifi_on_connect(wifi_callback_t callback);

// Register a callback for when WiFi disconnects.
void wifi_on_disconnect(wifi_callback_t callback);

// Connection statistics — for debugging field deployments.
// Updated automatically on connect/disconnect events. Zero overhead.
typedef struct {
    uint32_t connected_at;       // tick (ms) when last connected
    uint32_t uptime_ms;          // total time spent connected since boot
    uint32_t disconnects;        // number of disconnects since boot
    uint32_t longest_uptime_ms;  // longest single unbroken connection
    int      avg_rssi;           // rolling average signal strength (dBm)
} wifi_stats_t;

// Get current connection statistics.
wifi_stats_t wifi_get_stats(void);

#endif // VOLT_WIFI_H
