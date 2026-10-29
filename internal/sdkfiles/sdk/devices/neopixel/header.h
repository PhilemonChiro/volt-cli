#pragma once
#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// NeoPixel / WS2812B — Addressable RGB LEDs (single GPIO)
typedef struct { uint8_t gpio; uint16_t num_leds; uint8_t pixels[768]; } neopixel_t;
volt_error_t neopixel_init(neopixel_t *dev, uint8_t gpio, uint16_t num_leds);
void neopixel_set(neopixel_t *dev, uint16_t index, uint8_t r, uint8_t g, uint8_t b);
void neopixel_clear(neopixel_t *dev);
void neopixel_fill(neopixel_t *dev, uint8_t r, uint8_t g, uint8_t b);
void neopixel_show(neopixel_t *dev);
void neopixel_set_brightness(neopixel_t *dev, uint8_t brightness);
