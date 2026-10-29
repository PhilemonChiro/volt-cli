#pragma once
#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// (inserted before #endif by removing it first and re-adding)
// HD44780 LCD 16x2/20x4 via I2C (addr: 0x27)
typedef struct { uint8_t bus_id, addr, cols, rows, backlight; } hd44780_t;
volt_error_t hd44780_init(hd44780_t *dev, uint8_t bus_id, uint8_t addr, uint8_t cols, uint8_t rows);
void hd44780_clear(hd44780_t *dev);
void hd44780_set_cursor(hd44780_t *dev, uint8_t col, uint8_t row);
void hd44780_print(hd44780_t *dev, const char *text);
void hd44780_backlight(hd44780_t *dev, uint8_t on);
void hd44780_create_char(hd44780_t *dev, uint8_t slot, uint8_t r0, uint8_t r1, uint8_t r2, uint8_t r3, uint8_t r4, uint8_t r5, uint8_t r6, uint8_t r7);
void hd44780_write_char(hd44780_t *dev, uint8_t code);
void hd44780_reinit(hd44780_t *dev);
