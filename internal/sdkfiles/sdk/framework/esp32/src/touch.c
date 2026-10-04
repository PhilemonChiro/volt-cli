#include "volt/touch.h"
#include "volt/log.h"
#include "driver/touch_pad.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

// ESP32 touch pin mapping: GPIO → touch pad number
// T0=GPIO4, T1=GPIO0, T2=GPIO2, T3=GPIO15, T4=GPIO13,
// T5=GPIO12, T6=GPIO14, T7=GPIO27, T8=GPIO33, T9=GPIO32

#define MAX_TOUCH_PINS 10

static bool touch_initialized = false;
static uint16_t thresholds[40];  // indexed by GPIO number
static bool pin_active[40];

static int gpio_to_touch(uint8_t gpio)
{
    switch (gpio) {
        case 4:  return TOUCH_PAD_NUM0;
        case 0:  return TOUCH_PAD_NUM1;
        case 2:  return TOUCH_PAD_NUM2;
        case 15: return TOUCH_PAD_NUM3;
        case 13: return TOUCH_PAD_NUM4;
        case 12: return TOUCH_PAD_NUM5;
        case 14: return TOUCH_PAD_NUM6;
        case 27: return TOUCH_PAD_NUM7;
        case 33: return TOUCH_PAD_NUM8;
        case 32: return TOUCH_PAD_NUM9;
        default: return -1;
    }
}

static void touch_driver_init(void)
{
    if (touch_initialized) return;
    touch_pad_init();
    touch_pad_set_voltage(TOUCH_HVOLT_2V7, TOUCH_LVOLT_0V5, TOUCH_HVOLT_ATTEN_1V);
    touch_initialized = true;
}

volt_error_t touch_init(uint8_t pin)
{
    int pad = gpio_to_touch(pin);
    if (pad < 0) {
        log_error("touch", "GPIO %d is not a touch pin", pin);
        return VOLT_ERR_NOT_SUPPORTED;
    }

    touch_driver_init();
    touch_pad_config(pad, 0);

    // Wait for sensor to settle, then sample baseline
    vTaskDelay(50 / portTICK_PERIOD_MS);

    uint16_t baseline = 0;
    touch_pad_read(pad, &baseline);

    // Auto-threshold: 70% of untouched baseline
    thresholds[pin] = (uint16_t)(baseline * 0.7f);
    pin_active[pin] = true;

    log_debug("touch", "GPIO %d initialized (baseline=%d, threshold=%d)",
              pin, baseline, thresholds[pin]);
    return VOLT_OK;
}

uint16_t touch_read(uint8_t pin)
{
    int pad = gpio_to_touch(pin);
    if (pad < 0 || !pin_active[pin]) return 0;

    uint16_t value = 0;
    touch_pad_read(pad, &value);
    return value;
}

bool touch_is_touched(uint8_t pin)
{
    if (!pin_active[pin]) return false;
    uint16_t val = touch_read(pin);
    return val < thresholds[pin];
}

volt_error_t touch_set_threshold(uint8_t pin, uint16_t threshold)
{
    if (!pin_active[pin]) return VOLT_ERR_INVALID_PIN;
    thresholds[pin] = threshold;
    return VOLT_OK;
}

volt_error_t touch_release(uint8_t pin)
{
    if (!pin_active[pin]) return VOLT_OK;
    pin_active[pin] = false;
    thresholds[pin] = 0;
    return VOLT_OK;
}
