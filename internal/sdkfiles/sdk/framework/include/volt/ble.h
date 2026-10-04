#ifndef VOLT_BLE_H
#define VOLT_BLE_H

#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// ============================================================
// Bluetooth Low Energy — simplified for embedded devices.
//
// Two modes:
//   Peripheral: advertise a name, expose values, wait for connections.
//   Central: scan for nearby devices, read their values.
//
// ESP32: full BLE via NimBLE stack.
// RP2040: not supported (no BLE hardware; Pico W has WiFi only).
//
// The API hides GATT complexity. Each "value" you expose becomes a
// BLE characteristic automatically. No UUIDs, no service builders.
// ============================================================

// Maximum BLE values (characteristics) per device
#define BLE_MAX_VALUES 8

// Callback for when a connected device writes to a value
typedef void (*ble_write_callback_t)(uint8_t value_id, const uint8_t *data, uint16_t len);

// ---- Peripheral mode (most common for embedded) ----

// Start advertising with a device name. Other devices can discover you.
volt_error_t ble_advertise(const char *name);

// Stop advertising and disconnect any connected device.
volt_error_t ble_stop(void);

// Check if a central device is connected.
bool ble_is_connected(void);

// Expose an integer value over BLE. Returns a value_id (0-7).
// Connected devices can read this value at any time.
int ble_expose_int(const char *name, int initial_value);

// Expose a float value over BLE.
int ble_expose_float(const char *name, float initial_value);

// Expose a string value over BLE (max 20 bytes for BLE MTU).
int ble_expose_string(const char *name, const char *initial_value);

// Update an exposed value. Connected devices see the new value immediately.
volt_error_t ble_update_int(int value_id, int value);
volt_error_t ble_update_float(int value_id, float value);
volt_error_t ble_update_string(int value_id, const char *value);

// Register a callback for when a connected device writes to any value.
void ble_on_write(ble_write_callback_t callback);

// ---- Central mode (scanning) ----

// Scan for nearby BLE devices. Returns the number found.
// Results are stored internally — use ble_scan_name/rssi to read them.
int ble_scan(uint32_t duration_ms);

// Get the name of device at index (from last scan). NULL if no name.
const char *ble_scan_name(int index);

// Get the RSSI (signal strength) of device at index.
int ble_scan_rssi(int index);

#endif // VOLT_BLE_H
