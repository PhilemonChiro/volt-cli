#pragma once
#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// BH1750 — Light Sensor (addr: 0x23/0x5C)
typedef struct {
    uint8_t bus_id, addr;
} bh1750_t;
volt_error_t bh1750_init(bh1750_t *dev, uint8_t bus_id, uint8_t addr);
float bh1750_lux(bh1750_t *dev);
