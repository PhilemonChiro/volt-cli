#ifndef VOLT_PIN_H
#define VOLT_PIN_H

#include <stdint.h>
#include "volt/error.h"

// Output levels
#define HIGH 1
#define LOW  0

// Configure a pin as output. Must call before pin_write() or pin_toggle().
volt_error_t pin_set_output(uint8_t pin);

// Configure a pin as input (floating — no pull resistor).
volt_error_t pin_set_input(uint8_t pin);

// Configure a pin as input with internal pull-up resistor.
// Reads HIGH when nothing is connected, LOW when grounded.
// Use for buttons that connect to GND when pressed.
volt_error_t pin_set_input_pullup(uint8_t pin);

// Configure a pin as input with internal pull-down resistor.
// Reads LOW when nothing is connected, HIGH when connected to 3.3V.
volt_error_t pin_set_input_pulldown(uint8_t pin);

// Set an output pin HIGH or LOW. Returns error if pin is not output.
volt_error_t pin_write(uint8_t pin, uint8_t level);

// Flip an output pin: HIGH becomes LOW, LOW becomes HIGH.
// Returns error if pin is not output.
volt_error_t pin_toggle(uint8_t pin);

// Read the current level of any pin. Returns HIGH (1) or LOW (0).
// Works on both input and output pins.
int pin_read(uint8_t pin);

// Read an analog-capable pin as a raw 12-bit ADC value (0-4095).
// Returns 0 when the pin is not analog-capable or the target cannot read it.
int pin_read_analog(uint8_t pin);

// Register a callback that fires when a pin changes state.
// The callback runs in ISR context — keep it fast, no printf or malloc.
// Typically used to set a flag that a task checks.
typedef void (*pin_callback_t)(uint8_t pin, void *arg);
volt_error_t pin_on_change(uint8_t pin, pin_callback_t callback, void *arg);

// Release a pin back to unused state. Removes any callbacks.
volt_error_t pin_release(uint8_t pin);

// Configure a pin as PWM output. Default frequency ~1kHz, 8-bit resolution (0-255).
volt_error_t pin_set_pwm(uint8_t pin);

// Set PWM duty cycle (0-255). Pin must be configured with pin_set_pwm().
volt_error_t pin_pwm_write(uint8_t pin, uint8_t duty);

// Play a tone at a specific frequency (Hz). Pin must be PWM.
// Sets 50% duty cycle at the given frequency. 0 = silence.
volt_error_t pin_tone(uint8_t pin, uint32_t frequency_hz);

// Play a tone for a duration then stop.
void pin_tone_ms(uint8_t pin, uint32_t frequency_hz, uint32_t duration_ms);

// Stop any tone playing on this pin (set duty to 0).
void pin_no_tone(uint8_t pin);

// ---- Built-in tone patterns for HMI / interface feedback ----

// Quick click — button press feedback (short 2kHz tick)
void pin_tone_click(uint8_t pin);

// Success — two rising beeps (confirmation, save OK, connected)
void pin_tone_success(uint8_t pin);

// Error — three low buzzes (wrong input, failed, denied)
void pin_tone_error(uint8_t pin);

// Warning — two-tone alert (low battery, timeout approaching)
void pin_tone_warning(uint8_t pin);

// Startup chime — ascending three-note melody (boot, power on)
void pin_tone_startup(uint8_t pin);

// Shutdown — descending two-note (power off, sleep)
void pin_tone_shutdown(uint8_t pin);

// Notification — gentle double beep (message received, event)
void pin_tone_notify(uint8_t pin);

// Alarm — urgent siren pattern (intrusion, critical alert)
void pin_tone_alarm(uint8_t pin);

// Keypad — standard DTMF-like beep for each digit (0-9)
void pin_tone_keypad(uint8_t pin, uint8_t digit);

// Blink an output pin: toggle, wait interval_ms, toggle back.
// Non-blocking in protothread context (used with yield macros).
// For simple use in main or one-shot code.
void pin_blink(uint8_t pin, uint32_t interval_ms);

// Turn on for duration_ms, then turn off.
void pin_blink_once(uint8_t pin, uint32_t duration_ms);

// ---- DAC (Digital-to-Analog Converter) ----
// ESP32: 8-bit hardware DAC on GPIO 25 (ch0) and GPIO 26 (ch1).
// Outputs a true analog voltage (0 to 3.3V), not PWM.
// RP2040/STM32: no internal DAC — use an external I2C DAC (MCP4725).

// Configure a pin as DAC output. Only GPIO 25 and 26 on ESP32.
volt_error_t pin_set_dac(uint8_t pin);

// Set DAC raw value (0-255). 0 = 0V, 255 = 3.3V.
volt_error_t pin_dac_write(uint8_t pin, uint8_t value);

// Set DAC output to a specific voltage (0.0 - 3.3V).
// Internally maps to 0-255 range.
volt_error_t pin_dac_voltage(uint8_t pin, float voltage);

// Read the current DAC value (0-255).
uint8_t pin_dac_read(uint8_t pin);

#endif // VOLT_PIN_H
