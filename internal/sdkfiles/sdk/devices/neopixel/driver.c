#include "volt/gpio_devices.h"
/* NeoPixel / WS2812B Addressable RGB LEDs (single-wire, timing-critical)
   800kHz protocol: 0-bit = 400ns high + 850ns low, 1-bit = 800ns high + 450ns low */

#include "volt/pin.h"
#include "volt/log.h"
#include <string.h>
#ifdef ESP_PLATFORM
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#endif
#ifdef PICO_BOARD
#include "hardware/sync.h"
#endif

#define NEOPIXEL_MAX_LEDS 256

volt_error_t neopixel_init(neopixel_t *dev, uint8_t gpio, uint16_t num_leds)
{
    dev->gpio = gpio;
    dev->num_leds = (num_leds > NEOPIXEL_MAX_LEDS) ? NEOPIXEL_MAX_LEDS : num_leds;
    memset(dev->pixels, 0, sizeof(dev->pixels));
    pin_set_output(gpio);
    pin_write(gpio, 0);
    log_info("neopixel", "WS2812B: %d LEDs on GPIO %d", dev->num_leds, gpio);
    return VOLT_OK;
}

void neopixel_set(neopixel_t *dev, uint16_t index, uint8_t r, uint8_t g, uint8_t b)
{
    if (index >= dev->num_leds) return;
    uint16_t offset = index * 3;
    dev->pixels[offset]     = g;  // WS2812B uses GRB order
    dev->pixels[offset + 1] = r;
    dev->pixels[offset + 2] = b;
}

void neopixel_clear(neopixel_t *dev)
{
    memset(dev->pixels, 0, dev->num_leds * 3);
}

void neopixel_fill(neopixel_t *dev, uint8_t r, uint8_t g, uint8_t b)
{
    for (uint16_t i = 0; i < dev->num_leds; i++) {
        neopixel_set(dev, i, r, g, b);
    }
}

void neopixel_show(neopixel_t *dev)
{
    uint8_t gpio = dev->gpio;
    uint16_t total_bytes = dev->num_leds * 3;

    // Disable interrupts — WS2812B timing is corrupted by any ISR
#if defined(ESP_PLATFORM)
    portDISABLE_INTERRUPTS();
#elif defined(PICO_BOARD)
    uint32_t _irq_state = save_and_disable_interrupts();
#endif

    for (uint16_t i = 0; i < total_bytes; i++) {
        uint8_t byte = dev->pixels[i];
        for (int8_t bit = 7; bit >= 0; bit--) {
            if (byte & (1 << bit)) {
                // 1-bit: ~800ns high, ~450ns low
                pin_write(gpio, 1);
                for (volatile int d = 0; d < 8; d++);
                pin_write(gpio, 0);
                for (volatile int d = 0; d < 3; d++);
            } else {
                // 0-bit: ~400ns high, ~850ns low
                pin_write(gpio, 1);
                for (volatile int d = 0; d < 3; d++);
                pin_write(gpio, 0);
                for (volatile int d = 0; d < 8; d++);
            }
        }
    }

    // Re-enable interrupts
#if defined(ESP_PLATFORM)
    portENABLE_INTERRUPTS();
#elif defined(PICO_BOARD)
    restore_interrupts(_irq_state);
#endif
    // Latch: hold data line low for >50us (automatic — we're done sending)
}

void neopixel_set_brightness(neopixel_t *dev, uint8_t brightness)
{
    // Scale all pixels by brightness (0-255)
    float scale = brightness / 255.0f;
    for (uint16_t i = 0; i < dev->num_leds * 3; i++) {
        dev->pixels[i] = (uint8_t)(dev->pixels[i] * scale);
    }
}
