#ifndef VOLT_TOUCH_H
#define VOLT_TOUCH_H

#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// ============================================================
// Capacitive Touch Sensing
//
// ESP32: 10 hardware touch pins (T0-T9 = GPIO 4,0,2,15,13,12,14,27,33,32)
// RP2040/STM32: no capacitive touch hardware.
//
// Touch pins detect finger proximity by measuring capacitance changes.
// No external components needed — just wire to a pad or foil.
// ============================================================

// Configure a pin for capacitive touch sensing.
volt_error_t touch_init(uint8_t pin);

// Read raw touch value. Lower = touched, higher = untouched.
// Typical untouched: 50-100. Touched: 5-20.
uint16_t touch_read(uint8_t pin);

// Check if the pin is currently being touched (below threshold).
// Default threshold is set automatically on init by sampling the
// untouched baseline and using 70% of that value.
bool touch_is_touched(uint8_t pin);

// Set a custom touch threshold. Values below this = touched.
volt_error_t touch_set_threshold(uint8_t pin, uint16_t threshold);

// Release a touch pin back to unused state.
volt_error_t touch_release(uint8_t pin);

#endif // VOLT_TOUCH_H
