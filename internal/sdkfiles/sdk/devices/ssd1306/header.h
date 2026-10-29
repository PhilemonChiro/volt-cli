#pragma once
#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// SSD1306 — OLED Display 128x64 (addr: 0x3C/0x3D)
typedef struct {
    uint8_t bus_id, addr;
    uint8_t buffer[1024];
} ssd1306_t;
volt_error_t ssd1306_init(ssd1306_t *dev, uint8_t bus_id, uint8_t addr);
void ssd1306_clear(ssd1306_t *dev);
void ssd1306_text(ssd1306_t *dev, int x, int y, const char *text);
void ssd1306_show(ssd1306_t *dev);
