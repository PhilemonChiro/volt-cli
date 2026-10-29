#pragma once
#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// DHT11/DHT22 — Temperature + Humidity (single GPIO wire)
typedef struct { uint8_t gpio, type; float temperature, humidity; } dht_t;
volt_error_t dht_init(dht_t *dev, uint8_t gpio, uint8_t type);
float dht_temperature(dht_t *dev);
float dht_humidity(dht_t *dev);
