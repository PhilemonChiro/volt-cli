#ifndef VOLT_GPIO_DEVICES_H
#define VOLT_GPIO_DEVICES_H

#include <stdint.h>
#include "volt/error.h"

// ============================================================
// DHT11/DHT22 — Temperature + Humidity (single GPIO wire)
// ============================================================
typedef struct { uint8_t gpio, type; float temperature, humidity; } dht_t;
volt_error_t dht_init(dht_t *dev, uint8_t gpio, uint8_t type);
float dht_temperature(dht_t *dev);
float dht_humidity(dht_t *dev);

// ============================================================
// HC-SR04 — Ultrasonic Distance (2 GPIO: trigger + echo)
// ============================================================
typedef struct { uint8_t trig_pin, echo_pin; } hcsr04_t;
volt_error_t hcsr04_init(hcsr04_t *dev, uint8_t trig, uint8_t echo);
float hcsr04_distance_cm(hcsr04_t *dev);

// ============================================================
// NeoPixel / WS2812B — Addressable RGB LEDs (single GPIO)
// ============================================================
typedef struct { uint8_t gpio; uint16_t num_leds; uint8_t pixels[768]; } neopixel_t;
volt_error_t neopixel_init(neopixel_t *dev, uint8_t gpio, uint16_t num_leds);
void neopixel_set(neopixel_t *dev, uint16_t index, uint8_t r, uint8_t g, uint8_t b);
void neopixel_clear(neopixel_t *dev);
void neopixel_fill(neopixel_t *dev, uint8_t r, uint8_t g, uint8_t b);
void neopixel_show(neopixel_t *dev);
void neopixel_set_brightness(neopixel_t *dev, uint8_t brightness);

#endif // VOLT_GPIO_DEVICES_H
