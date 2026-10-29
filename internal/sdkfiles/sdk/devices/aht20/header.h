#pragma once
#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// AHT20 — Temperature + Humidity (addr: 0x38)
typedef struct {
    uint8_t bus_id, addr;
    float last_temperature, last_humidity;
} aht20_t;
volt_error_t aht20_init(aht20_t *dev, uint8_t bus_id, uint8_t addr);
float aht20_temperature(aht20_t *dev);
float aht20_humidity(aht20_t *dev);
