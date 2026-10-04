#include <stdbool.h>
#include <stdint.h>
#include "volt/pin.h"
#include "volt/log.h"
#include "driver/gpio.h"
#include "driver/ledc.h"
#include "driver/dac.h"
#include "driver/adc.h"
#include "esp_attr.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

// Track the mode of each pin so we can validate operations
typedef enum {
    PIN_MODE_UNUSED = 0,
    PIN_MODE_OUTPUT,
    PIN_MODE_INPUT,
    PIN_MODE_INPUT_PULLUP,
    PIN_MODE_INPUT_PULLDOWN,
    PIN_MODE_PWM,
    PIN_MODE_DAC,
} pin_mode_t;

// ESP32 has GPIO 0-39 (40 pins total)
#define PIN_COUNT 40

// LEDC channel tracking for PWM (ESP32 has 8 channels)
// -1 = not assigned. Must be initialized explicitly (static zero-init would give 0 = valid channel)
static int8_t pwm_channel_map[PIN_COUNT] = {
    [0 ... 39] = -1
};
static uint8_t next_pwm_channel = 0;
static pin_mode_t pin_modes[PIN_COUNT];

// Track output state ourselves — gpio_get_level() reads the INPUT register
// which may not reflect the output on all ESP32 pins/revisions.
static uint8_t pin_output_state[PIN_COUNT];

// Whether the GPIO ISR service has been installed (one-time init)
static bool isr_service_installed = false;
static bool adc_width_configured = false;
static bool adc1_channel_configured[ADC1_CHANNEL_MAX];
static bool adc2_channel_configured[ADC2_CHANNEL_MAX];

// ISR wrapper: ESP-IDF gives us (void *arg), but our callback wants (pin, arg).
// We store the pin number and user callback in a small struct per pin.
typedef struct {
    pin_callback_t callback;
    void *arg;
    uint8_t pin;
} pin_isr_ctx_t;

// volatile: ISR reads these from interrupt context on a different core
static volatile pin_isr_ctx_t isr_contexts[PIN_COUNT];

// This is the actual ISR that ESP-IDF calls — it unpacks and calls the user's callback
static void IRAM_ATTR pin_isr_wrapper(void *arg)
{
    volatile pin_isr_ctx_t *ctx = (volatile pin_isr_ctx_t *)arg;
    ctx->callback(ctx->pin, ctx->arg);
}

// Returns false for pins that would crash the chip if reconfigured
static bool is_valid_pin(uint8_t pin)
{
    if (pin >= PIN_COUNT) return false;

    // GPIO 6-11 are connected to SPI flash — never touch these
    if (pin >= 6 && pin <= 11) return false;

    return true;
}

// Returns false for pins that cannot be used as output (input-only pins)
static bool can_output(uint8_t pin)
{
    // GPIO 34-39 are input-only — no output driver in hardware
    return pin <= 33;
}

// Internal helper: configure a pin with ESP-IDF
static volt_error_t pin_configure(uint8_t pin, gpio_mode_t mode,
                                  bool pullup, bool pulldown)
{
    if (!is_valid_pin(pin)) {
        log_error("pin", "GPIO %d is invalid or reserved for flash", pin);
        return VOLT_ERR_INVALID_PIN;
    }

    // Allow switching among GPIO directions at runtime: bit-banged protocols
    // (DHT, 1-Wire, ultrasonic) legitimately toggle a pin between output and
    // input. Two-device-on-one-pin conflicts are caught at compile time by the
    // hardware validator, so the only thing to guard here is silently stealing a
    // pin already held by a peripheral (PWM/DAC) — release it first.
    if (pin_modes[pin] == PIN_MODE_PWM || pin_modes[pin] == PIN_MODE_DAC) {
        log_error("pin", "GPIO %d is in use by a peripheral (PWM/DAC) — release it first", pin);
        return VOLT_ERR_PIN_IN_USE;
    }

    gpio_config_t cfg = {
        .pin_bit_mask = (1ULL << pin),
        .mode = mode,
        .pull_up_en = pullup ? GPIO_PULLUP_ENABLE : GPIO_PULLUP_DISABLE,
        .pull_down_en = pulldown ? GPIO_PULLDOWN_ENABLE : GPIO_PULLDOWN_DISABLE,
        .intr_type = GPIO_INTR_DISABLE,
    };

    esp_err_t err = gpio_config(&cfg);
    if (err != ESP_OK) {
        log_error("pin", "GPIO %d config failed (esp_err=%d)", pin, err);
        return VOLT_ERR_PIN_CONFIG_FAILED;
    }

    return VOLT_OK;
}

volt_error_t pin_set_output(uint8_t pin)
{
    if (!can_output(pin)) {
        log_error("pin", "GPIO %d is input-only, cannot be output", pin);
        return VOLT_ERR_INVALID_PIN;
    }

    volt_error_t result = pin_configure(pin, GPIO_MODE_OUTPUT, false, false);
    if (result == VOLT_OK) {
        pin_modes[pin] = PIN_MODE_OUTPUT;
        log_debug("pin", "GPIO %d configured as output", pin);
    }
    return result;
}

volt_error_t pin_set_input(uint8_t pin)
{
    volt_error_t result = pin_configure(pin, GPIO_MODE_INPUT, false, false);
    if (result == VOLT_OK) {
        pin_modes[pin] = PIN_MODE_INPUT;
        log_debug("pin", "GPIO %d configured as input", pin);
    }
    return result;
}

volt_error_t pin_set_input_pullup(uint8_t pin)
{
    // GPIO 34-39 have no internal pull resistors
    if (pin >= 34) {
        log_error("pin", "GPIO %d has no internal pull-up (input-only pin)", pin);
        return VOLT_ERR_NOT_SUPPORTED;
    }

    volt_error_t result = pin_configure(pin, GPIO_MODE_INPUT, true, false);
    if (result == VOLT_OK) {
        pin_modes[pin] = PIN_MODE_INPUT_PULLUP;
        log_debug("pin", "GPIO %d configured as input with pull-up", pin);
    }
    return result;
}

volt_error_t pin_set_input_pulldown(uint8_t pin)
{
    // GPIO 34-39 have no internal pull resistors
    if (pin >= 34) {
        log_error("pin", "GPIO %d has no internal pull-down (input-only pin)", pin);
        return VOLT_ERR_NOT_SUPPORTED;
    }

    volt_error_t result = pin_configure(pin, GPIO_MODE_INPUT, false, true);
    if (result == VOLT_OK) {
        pin_modes[pin] = PIN_MODE_INPUT_PULLDOWN;
        log_debug("pin", "GPIO %d configured as input with pull-down", pin);
    }
    return result;
}

volt_error_t pin_write(uint8_t pin, uint8_t level)
{
    if (pin >= PIN_COUNT) return VOLT_ERR_INVALID_PIN;

    if (pin_modes[pin] != PIN_MODE_OUTPUT) {
        log_error("pin", "GPIO %d is not configured as output", pin);
        return VOLT_ERR_WRONG_PIN_MODE;
    }

    uint8_t val = level ? 1 : 0;
    gpio_set_level(pin, val);
    pin_output_state[pin] = val;
    return VOLT_OK;
}

volt_error_t pin_toggle(uint8_t pin)
{
    if (pin >= PIN_COUNT) return VOLT_ERR_INVALID_PIN;

    if (pin_modes[pin] != PIN_MODE_OUTPUT) {
        log_error("pin", "GPIO %d is not configured as output", pin);
        return VOLT_ERR_WRONG_PIN_MODE;
    }

    // Use tracked state — gpio_get_level reads INPUT register which
    // doesn't reflect output state on output-only configured pins.
    pin_output_state[pin] = !pin_output_state[pin];
    gpio_set_level(pin, pin_output_state[pin]);
    return VOLT_OK;
}

int pin_read(uint8_t pin)
{
    if (pin >= PIN_COUNT) return LOW;
    return gpio_get_level(pin);
}

static bool pin_to_adc1_channel(uint8_t pin, adc1_channel_t *channel)
{
    switch (pin) {
    case 36: *channel = ADC1_CHANNEL_0; return true;
    case 37: *channel = ADC1_CHANNEL_1; return true;
    case 38: *channel = ADC1_CHANNEL_2; return true;
    case 39: *channel = ADC1_CHANNEL_3; return true;
    case 32: *channel = ADC1_CHANNEL_4; return true;
    case 33: *channel = ADC1_CHANNEL_5; return true;
    case 34: *channel = ADC1_CHANNEL_6; return true;
    case 35: *channel = ADC1_CHANNEL_7; return true;
    default: return false;
    }
}

static bool pin_to_adc2_channel(uint8_t pin, adc2_channel_t *channel)
{
    switch (pin) {
    case 4:  *channel = ADC2_CHANNEL_0; return true;
    case 0:  *channel = ADC2_CHANNEL_1; return true;
    case 2:  *channel = ADC2_CHANNEL_2; return true;
    case 15: *channel = ADC2_CHANNEL_3; return true;
    case 13: *channel = ADC2_CHANNEL_4; return true;
    case 12: *channel = ADC2_CHANNEL_5; return true;
    case 14: *channel = ADC2_CHANNEL_6; return true;
    case 27: *channel = ADC2_CHANNEL_7; return true;
    case 25: *channel = ADC2_CHANNEL_8; return true;
    case 26: *channel = ADC2_CHANNEL_9; return true;
    default: return false;
    }
}

int pin_read_analog(uint8_t pin)
{
    if (!is_valid_pin(pin)) return 0;

    adc1_channel_t adc1;
    if (pin_to_adc1_channel(pin, &adc1)) {
        if (!adc_width_configured) {
            adc1_config_width(ADC_WIDTH_BIT_12);
            adc_width_configured = true;
        }
        if (!adc1_channel_configured[adc1]) {
            adc1_config_channel_atten(adc1, ADC_ATTEN_DB_12);
            adc1_channel_configured[adc1] = true;
        }
        return adc1_get_raw(adc1);
    }

    adc2_channel_t adc2;
    if (pin_to_adc2_channel(pin, &adc2)) {
        if (!adc2_channel_configured[adc2]) {
            adc2_config_channel_atten(adc2, ADC_ATTEN_DB_12);
            adc2_channel_configured[adc2] = true;
        }
        int raw = 0;
        if (adc2_get_raw(adc2, ADC_WIDTH_BIT_12, &raw) != ESP_OK) {
            return 0;
        }
        return raw;
    }

    return 0;
}

volt_error_t pin_on_change(uint8_t pin, pin_callback_t callback, void *arg)
{
    if (!is_valid_pin(pin)) return VOLT_ERR_INVALID_PIN;

    // Install the GPIO ISR service once on first use
    if (!isr_service_installed) {
        esp_err_t err = gpio_install_isr_service(0);
        if (err != ESP_OK && err != ESP_ERR_INVALID_STATE) {
            // ESP_ERR_INVALID_STATE means already installed — that's fine
            log_error("pin", "ISR service install failed (esp_err=%d)", err);
            return VOLT_ERR_PIN_CONFIG_FAILED;
        }
        isr_service_installed = true;
    }

    // Store the user's callback and pin number in the context
    isr_contexts[pin].callback = callback;
    isr_contexts[pin].arg = arg;
    isr_contexts[pin].pin = pin;

    // Enable interrupts on both edges (rising and falling)
    gpio_set_intr_type(pin, GPIO_INTR_ANYEDGE);
    esp_err_t err = gpio_isr_handler_add(pin, pin_isr_wrapper, (void *)&isr_contexts[pin]);
    if (err != ESP_OK) {
        log_error("pin", "GPIO %d ISR handler add failed (esp_err=%d)", pin, err);
        return VOLT_ERR_PIN_CONFIG_FAILED;
    }

    log_debug("pin", "GPIO %d interrupt handler registered", pin);
    return VOLT_OK;
}

volt_error_t pin_release(uint8_t pin)
{
    if (pin >= PIN_COUNT) return VOLT_ERR_INVALID_PIN;

    if (pin_modes[pin] == PIN_MODE_UNUSED) {
        return VOLT_OK;  // already unused, nothing to do
    }

    // Remove ISR handler if any
    gpio_isr_handler_remove(pin);

    // Reset pin to default state (ESP32 only — ESP8266 doesn't have gpio_reset_pin)
#ifdef CONFIG_IDF_TARGET_ESP32
    gpio_reset_pin(pin);
#endif

    pin_modes[pin] = PIN_MODE_UNUSED;
    log_debug("pin", "GPIO %d released", pin);
    return VOLT_OK;
}

volt_error_t pin_set_pwm(uint8_t pin)
{
    if (!is_valid_pin(pin)) return VOLT_ERR_INVALID_PIN;
    if (!can_output(pin)) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] != PIN_MODE_UNUSED) return VOLT_ERR_PIN_IN_USE;
    if (next_pwm_channel >= 8) {
        log_error("pin", "No LEDC channels left for GPIO %d (max 8 PWM pins)", pin);
        return VOLT_ERR_NOT_SUPPORTED;
    }

    uint8_t ch = next_pwm_channel++;
    pwm_channel_map[pin] = ch;

    // Each pair of channels shares a timer (0-1 → T0, 2-3 → T1, 4-5 → T2, 6-7 → T3)
    // This lets pin_tone() change frequency on one pin without affecting others
    uint8_t timer_id = ch / 2;

    ledc_timer_config_t timer_cfg = {
        .speed_mode = LEDC_LOW_SPEED_MODE,
        .duty_resolution = LEDC_TIMER_8_BIT,  // 0-255
        .timer_num = timer_id,
        .freq_hz = 1000,  // 1kHz default
        .clk_cfg = LEDC_AUTO_CLK,
    };
    ledc_timer_config(&timer_cfg);

    // Configure LEDC channel
    ledc_channel_config_t ch_cfg = {
        .speed_mode = LEDC_LOW_SPEED_MODE,
        .channel = ch,
        .timer_sel = timer_id,
        .gpio_num = pin,
        .duty = 0,
        .hpoint = 0,
    };
    ledc_channel_config(&ch_cfg);

    pin_modes[pin] = PIN_MODE_PWM;
    log_debug("pin", "GPIO %d configured as PWM (LEDC ch%d, 1kHz, 8-bit)", pin, ch);
    return VOLT_OK;
}

volt_error_t pin_pwm_write(uint8_t pin, uint8_t duty)
{
    if (pin >= PIN_COUNT) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] != PIN_MODE_PWM) return VOLT_ERR_WRONG_PIN_MODE;

    int8_t ch = pwm_channel_map[pin];
    if (ch < 0) return VOLT_ERR_INTERNAL;

    ledc_set_duty(LEDC_LOW_SPEED_MODE, ch, duty);
    ledc_update_duty(LEDC_LOW_SPEED_MODE, ch);
    return VOLT_OK;
}

volt_error_t pin_tone(uint8_t pin, uint32_t frequency_hz)
{
    if (pin >= PIN_COUNT) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] != PIN_MODE_PWM) return VOLT_ERR_WRONG_PIN_MODE;

    int8_t ch = pwm_channel_map[pin];
    if (ch < 0) return VOLT_ERR_INTERNAL;

    if (frequency_hz == 0) {
        ledc_set_duty(LEDC_LOW_SPEED_MODE, ch, 0);
        ledc_update_duty(LEDC_LOW_SPEED_MODE, ch);
    } else {
        uint8_t timer_id = ch / 2;
        ledc_set_freq(LEDC_LOW_SPEED_MODE, timer_id, frequency_hz);
        ledc_set_duty(LEDC_LOW_SPEED_MODE, ch, 128); // 50% duty = square wave
        ledc_update_duty(LEDC_LOW_SPEED_MODE, ch);
    }
    return VOLT_OK;
}

void pin_tone_ms(uint8_t pin, uint32_t frequency_hz, uint32_t duration_ms)
{
    pin_tone(pin, frequency_hz);
    vTaskDelay(duration_ms / portTICK_PERIOD_MS);
    pin_tone(pin, 0);
}

void pin_no_tone(uint8_t pin)
{
    pin_tone(pin, 0);
}

// ---- Built-in tone patterns ----

void pin_tone_click(uint8_t pin)
{
    pin_tone_ms(pin, 2000, 30);
}

void pin_tone_success(uint8_t pin)
{
    pin_tone_ms(pin, 880, 100);
    vTaskDelay(30 / portTICK_PERIOD_MS);
    pin_tone_ms(pin, 1760, 150);
}

void pin_tone_error(uint8_t pin)
{
    pin_tone_ms(pin, 200, 100);
    vTaskDelay(50 / portTICK_PERIOD_MS);
    pin_tone_ms(pin, 200, 100);
    vTaskDelay(50 / portTICK_PERIOD_MS);
    pin_tone_ms(pin, 200, 200);
}

void pin_tone_warning(uint8_t pin)
{
    pin_tone_ms(pin, 600, 150);
    vTaskDelay(100 / portTICK_PERIOD_MS);
    pin_tone_ms(pin, 800, 150);
}

void pin_tone_startup(uint8_t pin)
{
    pin_tone_ms(pin, 523, 100);  // C5
    vTaskDelay(30 / portTICK_PERIOD_MS);
    pin_tone_ms(pin, 659, 100);  // E5
    vTaskDelay(30 / portTICK_PERIOD_MS);
    pin_tone_ms(pin, 784, 200);  // G5
}

void pin_tone_shutdown(uint8_t pin)
{
    pin_tone_ms(pin, 784, 100);  // G5
    vTaskDelay(30 / portTICK_PERIOD_MS);
    pin_tone_ms(pin, 523, 200);  // C5
}

void pin_tone_notify(uint8_t pin)
{
    pin_tone_ms(pin, 1200, 80);
    vTaskDelay(80 / portTICK_PERIOD_MS);
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
    // DTMF-inspired frequencies per digit
    static const uint16_t freqs[] = {
        941,  // 0
        697,  // 1
        697,  // 2
        697,  // 3
        770,  // 4
        770,  // 5
        770,  // 6
        852,  // 7
        852,  // 8
        852,  // 9
    };
    if (digit > 9) return;
    pin_tone_ms(pin, freqs[digit], 100);
}

void pin_blink(uint8_t pin, uint32_t interval_ms)
{
    pin_toggle(pin);
    vTaskDelay(interval_ms / portTICK_PERIOD_MS);
    pin_toggle(pin);
}

void pin_blink_once(uint8_t pin, uint32_t duration_ms)
{
    pin_write(pin, HIGH);
    vTaskDelay(duration_ms / portTICK_PERIOD_MS);
    pin_write(pin, LOW);
}

// ---- DAC ----
// ESP32 has 2 hardware DAC channels:
//   GPIO 25 → DAC channel 0
//   GPIO 26 → DAC channel 1

static uint8_t dac_values[PIN_COUNT];

static int8_t pin_to_dac_channel(uint8_t pin)
{
    if (pin == 25) return DAC_CHAN_0;
    if (pin == 26) return DAC_CHAN_1;
    return -1;
}

volt_error_t pin_set_dac(uint8_t pin)
{
    if (pin != 25 && pin != 26) {
        log_error("pin", "GPIO %d has no DAC — only GPIO 25 and 26", pin);
        return VOLT_ERR_NOT_SUPPORTED;
    }
    if (pin_modes[pin] != PIN_MODE_UNUSED) return VOLT_ERR_PIN_IN_USE;

    int8_t ch = pin_to_dac_channel(pin);
    if (ch < 0) return VOLT_ERR_INTERNAL;
    esp_err_t err = dac_output_enable(ch);
    if (err != ESP_OK) {
        log_error("pin", "DAC enable failed on GPIO %d (esp_err=%d)", pin, err);
        return VOLT_ERR_PIN_CONFIG_FAILED;
    }

    dac_output_voltage(ch, 0);
    dac_values[pin] = 0;
    pin_modes[pin] = PIN_MODE_DAC;
    log_debug("pin", "GPIO %d configured as DAC output", pin);
    return VOLT_OK;
}

volt_error_t pin_dac_write(uint8_t pin, uint8_t value)
{
    if (pin >= PIN_COUNT) return VOLT_ERR_INVALID_PIN;
    if (pin_modes[pin] != PIN_MODE_DAC) return VOLT_ERR_WRONG_PIN_MODE;

    int8_t ch = pin_to_dac_channel(pin);
    if (ch < 0) return VOLT_ERR_INTERNAL;
    dac_output_voltage(ch, value);
    dac_values[pin] = value;
    return VOLT_OK;
}

volt_error_t pin_dac_voltage(uint8_t pin, float voltage)
{
    if (voltage < 0.0f) voltage = 0.0f;
    if (voltage > 3.3f) voltage = 3.3f;

    uint8_t raw = (uint8_t)(voltage / 3.3f * 255.0f);
    return pin_dac_write(pin, raw);
}

uint8_t pin_dac_read(uint8_t pin)
{
    if (pin >= PIN_COUNT) return 0;
    if (pin_modes[pin] != PIN_MODE_DAC) return 0;
    return dac_values[pin];
}
