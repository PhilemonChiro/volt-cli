#include <stdbool.h>
#include <stdint.h>
#include "volt/pin.h"
#include "volt/log.h"
#include "pico/stdlib.h"
#include "hardware/gpio.h"
#include "hardware/adc.h"
#include "hardware/pwm.h"

static bool adc_initialized = false;

typedef enum {
    PIN_MODE_UNUSED = 0,
    PIN_MODE_OUTPUT,
    PIN_MODE_INPUT,
    PIN_MODE_INPUT_PULLUP,
    PIN_MODE_INPUT_PULLDOWN,
    PIN_MODE_PWM,
} pin_mode_t;

// RP2040 has GP0-GP29 (30 pins)
#define PIN_COUNT 30
static pin_mode_t pin_modes[PIN_COUNT];
static uint8_t pin_output_state[PIN_COUNT];

// ISR context per pin
typedef struct {
    pin_callback_t callback;
    void *arg;
    uint8_t pin;
} pin_isr_ctx_t;

static volatile pin_isr_ctx_t isr_contexts[PIN_COUNT];

// Single GPIO IRQ handler — dispatches to per-pin callbacks
static void gpio_irq_handler(uint gpio, uint32_t events)
{
    if (gpio < PIN_COUNT && isr_contexts[gpio].callback) {
        isr_contexts[gpio].callback(isr_contexts[gpio].pin, isr_contexts[gpio].arg);
    }
}

static bool is_valid_pin(uint8_t pin)
{
    if (pin >= PIN_COUNT) return false;
    if (pin == 29) return false; // GP29 = internal VSYS voltage divider
    return true;
}

volt_error_t pin_set_output(uint8_t pin)
{
    if (!is_valid_pin(pin)) return VOLT_ERR_INVALID_PIN;
    // Allow GPIO direction switches (bit-banged DHT/1-Wire); only block stealing
    // a pin held by a peripheral. Pin conflicts are caught at compile time.
    if (pin_modes[pin] == PIN_MODE_PWM) return VOLT_ERR_PIN_IN_USE;

    gpio_init(pin);
    gpio_set_dir(pin, GPIO_OUT);
    pin_modes[pin] = PIN_MODE_OUTPUT;
    return VOLT_OK;
}

volt_error_t pin_set_input(uint8_t pin)
{
    if (!is_valid_pin(pin)) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] == PIN_MODE_PWM) return VOLT_ERR_PIN_IN_USE;

    gpio_init(pin);
    gpio_set_dir(pin, GPIO_IN);
    gpio_disable_pulls(pin);
    pin_modes[pin] = PIN_MODE_INPUT;
    return VOLT_OK;
}

volt_error_t pin_set_input_pullup(uint8_t pin)
{
    if (!is_valid_pin(pin)) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] == PIN_MODE_PWM) return VOLT_ERR_PIN_IN_USE;

    gpio_init(pin);
    gpio_set_dir(pin, GPIO_IN);
    gpio_pull_up(pin);
    pin_modes[pin] = PIN_MODE_INPUT_PULLUP;
    return VOLT_OK;
}

volt_error_t pin_set_input_pulldown(uint8_t pin)
{
    if (!is_valid_pin(pin)) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] == PIN_MODE_PWM) return VOLT_ERR_PIN_IN_USE;

    gpio_init(pin);
    gpio_set_dir(pin, GPIO_IN);
    gpio_pull_down(pin);
    pin_modes[pin] = PIN_MODE_INPUT_PULLDOWN;
    return VOLT_OK;
}

volt_error_t pin_write(uint8_t pin, uint8_t level)
{
    if (pin >= PIN_COUNT) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] != PIN_MODE_OUTPUT) return VOLT_ERR_WRONG_PIN_MODE;

    uint8_t val = level ? 1 : 0;
    gpio_put(pin, val);
    pin_output_state[pin] = val;
    return VOLT_OK;
}

volt_error_t pin_toggle(uint8_t pin)
{
    if (pin >= PIN_COUNT) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] != PIN_MODE_OUTPUT) return VOLT_ERR_WRONG_PIN_MODE;

    pin_output_state[pin] = !pin_output_state[pin];
    gpio_put(pin, pin_output_state[pin]);
    return VOLT_OK;
}

int pin_read(uint8_t pin)
{
    if (pin >= PIN_COUNT) return LOW;

    return gpio_get(pin) ? HIGH : LOW;
}

int pin_read_analog(uint8_t pin)
{
    if (pin < 26 || pin > 28 || pin_modes[pin] == PIN_MODE_PWM) {
        return 0;
    }
    if (!adc_initialized) {
        adc_init();
        adc_initialized = true;
    }
    adc_gpio_init(pin);
    adc_select_input(pin - 26);
    return adc_read(); // 12-bit: 0-4095
}

volt_error_t pin_on_change(uint8_t pin, pin_callback_t callback, void *arg)
{
    if (!is_valid_pin(pin)) return VOLT_ERR_INVALID_PIN;

    isr_contexts[pin].callback = callback;
    isr_contexts[pin].arg = arg;
    isr_contexts[pin].pin = pin;

    gpio_set_irq_enabled_with_callback(pin, GPIO_IRQ_EDGE_RISE | GPIO_IRQ_EDGE_FALL,
                                       true, gpio_irq_handler);
    return VOLT_OK;
}

volt_error_t pin_release(uint8_t pin)
{
    if (pin >= PIN_COUNT) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] == PIN_MODE_UNUSED) return VOLT_OK;

    gpio_set_irq_enabled(pin, GPIO_IRQ_EDGE_RISE | GPIO_IRQ_EDGE_FALL, false);
    isr_contexts[pin].callback = NULL;
    gpio_deinit(pin);
    pin_modes[pin] = PIN_MODE_UNUSED;
    return VOLT_OK;
}

volt_error_t pin_set_pwm(uint8_t pin)
{
    if (!is_valid_pin(pin)) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] != PIN_MODE_UNUSED) return VOLT_ERR_PIN_IN_USE;

    // RP2040: each GPIO maps to a PWM slice (pin/2) and channel (pin%2)
    gpio_set_function(pin, GPIO_FUNC_PWM);
    uint slice = pwm_gpio_to_slice_num(pin);

    // Configure: ~1kHz frequency, 8-bit resolution (wrap at 255)
    pwm_set_wrap(slice, 255);
    pwm_set_clkdiv(slice, 488.28f);  // 125MHz / 488.28 / 256 ≈ 1kHz
    pwm_set_enabled(slice, true);
    pwm_set_gpio_level(pin, 0);  // start at 0% duty

    pin_modes[pin] = PIN_MODE_PWM;
    return VOLT_OK;
}

volt_error_t pin_pwm_write(uint8_t pin, uint8_t duty)
{
    if (pin >= PIN_COUNT) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] != PIN_MODE_PWM) return VOLT_ERR_WRONG_PIN_MODE;

    pwm_set_gpio_level(pin, duty);
    return VOLT_OK;
}

volt_error_t pin_tone(uint8_t pin, uint32_t frequency_hz)
{
    if (pin >= PIN_COUNT) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] != PIN_MODE_PWM) return VOLT_ERR_WRONG_PIN_MODE;

    uint slice = pwm_gpio_to_slice_num(pin);

    if (frequency_hz == 0) {
        pwm_set_gpio_level(pin, 0);
    } else {
        // RP2040: 125MHz system clock
        // wrap = (125MHz / frequency) - 1, clkdiv = 1
        uint32_t wrap = (125000000 / frequency_hz) - 1;
        float clkdiv = 1.0f;

        // If wrap > 65535, increase clock divider
        while (wrap > 65535 && clkdiv < 256.0f) {
            clkdiv *= 2.0f;
            wrap = (uint32_t)(125000000.0f / (clkdiv * frequency_hz)) - 1;
        }

        pwm_set_clkdiv(slice, clkdiv);
        pwm_set_wrap(slice, (uint16_t)wrap);
        pwm_set_gpio_level(pin, wrap / 2);  // 50% duty = square wave
    }
    return VOLT_OK;
}

void pin_tone_ms(uint8_t pin, uint32_t frequency_hz, uint32_t duration_ms)
{
    pin_tone(pin, frequency_hz);
    sleep_ms(duration_ms);
    pin_tone(pin, 0);
}

void pin_no_tone(uint8_t pin)
{
    pin_tone(pin, 0);
}

// ---- Built-in tone patterns ----

void pin_tone_click(uint8_t pin)      { pin_tone_ms(pin, 2000, 30); }

void pin_tone_success(uint8_t pin)
{
    pin_tone_ms(pin, 880, 100);
    sleep_ms(30);
    pin_tone_ms(pin, 1760, 150);
}

void pin_tone_error(uint8_t pin)
{
    pin_tone_ms(pin, 200, 100);
    sleep_ms(50);
    pin_tone_ms(pin, 200, 100);
    sleep_ms(50);
    pin_tone_ms(pin, 200, 200);
}

void pin_tone_warning(uint8_t pin)
{
    pin_tone_ms(pin, 600, 150);
    sleep_ms(100);
    pin_tone_ms(pin, 800, 150);
}

void pin_tone_startup(uint8_t pin)
{
    pin_tone_ms(pin, 523, 100);
    sleep_ms(30);
    pin_tone_ms(pin, 659, 100);
    sleep_ms(30);
    pin_tone_ms(pin, 784, 200);
}

void pin_tone_shutdown(uint8_t pin)
{
    pin_tone_ms(pin, 784, 100);
    sleep_ms(30);
    pin_tone_ms(pin, 523, 200);
}

void pin_tone_notify(uint8_t pin)
{
    pin_tone_ms(pin, 1200, 80);
    sleep_ms(80);
    pin_tone_ms(pin, 1200, 80);
}

void pin_tone_alarm(uint8_t pin)
{
    for (int i = 0; i < 3; i++) {
        pin_tone_ms(pin, 800, 200);
        pin_tone_ms(pin, 1200, 200);
    }
}

void pin_tone_keypad(uint8_t pin, uint8_t digit)
{
    static const uint16_t freqs[] = { 941, 697, 697, 697, 770, 770, 770, 852, 852, 852 };
    if (digit > 9) return;
    pin_tone_ms(pin, freqs[digit], 100);
}

void pin_blink(uint8_t pin, uint32_t interval_ms)
{
    pin_toggle(pin);
    sleep_ms(interval_ms);
    pin_toggle(pin);
}

void pin_blink_once(uint8_t pin, uint32_t duration_ms)
{
    pin_write(pin, HIGH);
    sleep_ms(duration_ms);
    pin_write(pin, LOW);
}

// ---- DAC ----
// RP2040 has no hardware DAC. These return VOLT_ERR_NOT_SUPPORTED.
// Use an external I2C DAC (MCP4725) or PWM with an RC filter instead.

volt_error_t pin_set_dac(uint8_t pin)
{
    return VOLT_ERR_NOT_SUPPORTED;
}

volt_error_t pin_dac_write(uint8_t pin, uint8_t value)
{
    return VOLT_ERR_NOT_SUPPORTED;
}

volt_error_t pin_dac_voltage(uint8_t pin, float voltage)
{
    return VOLT_ERR_NOT_SUPPORTED;
}

uint8_t pin_dac_read(uint8_t pin)
{
    return 0;
}
