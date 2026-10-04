#include "volt/ble.h"

// RP2040 has no BLE hardware.
// Pico W has WiFi (CYW43) but not BLE.

volt_error_t ble_advertise(const char *name)          { return VOLT_ERR_NOT_SUPPORTED; }
volt_error_t ble_stop(void)                           { return VOLT_OK; }
bool ble_is_connected(void)                           { return false; }
int ble_expose_int(const char *name, int val)         { return -1; }
int ble_expose_float(const char *name, float val)     { return -1; }
int ble_expose_string(const char *name, const char *v){ return -1; }
volt_error_t ble_update_int(int id, int val)          { return VOLT_ERR_NOT_SUPPORTED; }
volt_error_t ble_update_float(int id, float val)      { return VOLT_ERR_NOT_SUPPORTED; }
volt_error_t ble_update_string(int id, const char *v) { return VOLT_ERR_NOT_SUPPORTED; }
void ble_on_write(ble_write_callback_t cb)            { }
int ble_scan(uint32_t ms)                             { return 0; }
const char *ble_scan_name(int idx)                    { return 0; }
int ble_scan_rssi(int idx)                            { return 0; }
