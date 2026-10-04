#include "volt/touch.h"

// RP2040 has no capacitive touch hardware.
// All functions return VOLT_ERR_NOT_SUPPORTED.

volt_error_t touch_init(uint8_t pin)       { return VOLT_ERR_NOT_SUPPORTED; }
uint16_t touch_read(uint8_t pin)           { return 0; }
bool touch_is_touched(uint8_t pin)         { return false; }
volt_error_t touch_set_threshold(uint8_t pin, uint16_t threshold) { return VOLT_ERR_NOT_SUPPORTED; }
volt_error_t touch_release(uint8_t pin)    { (void)pin; return VOLT_ERR_NOT_SUPPORTED; }
