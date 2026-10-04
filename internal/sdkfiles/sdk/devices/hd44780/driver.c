/* HD44780 LCD 16x2 / 20x4 via I2C PCF8574 backpack
   Most common LCD display in embedded projects */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/task.h"
#include <string.h>

// PCF8574 bit mapping for common I2C LCD backpacks
#define LCD_RS    0x01
#define LCD_EN    0x04
#define LCD_BL    0x08  // backlight

static void lcd_write4(hd44780_t *dev, uint8_t data)
{
    uint8_t bl = dev->backlight ? LCD_BL : 0;
    uint8_t hi = data | bl;
    // Pulse enable
    uint8_t en_hi = hi | LCD_EN;
    uint8_t en_lo = hi & ~LCD_EN;
    volt_i2c_write(dev->bus_id, dev->addr, &en_hi, 1);
    volt_i2c_write(dev->bus_id, dev->addr, &en_lo, 1);
}

static void lcd_send(hd44780_t *dev, uint8_t value, uint8_t mode)
{
    uint8_t hi = (value & 0xF0) | mode;
    uint8_t lo = ((value << 4) & 0xF0) | mode;
    lcd_write4(dev, hi);
    lcd_write4(dev, lo);
}

static void lcd_command(hd44780_t *dev, uint8_t cmd)
{
    lcd_send(dev, cmd, 0);
    if (cmd <= 0x03) volt_delay_ms(2); // clear/home need extra time
}

volt_error_t hd44780_init(hd44780_t *dev, uint8_t bus_id, uint8_t addr,
                          uint8_t cols, uint8_t rows)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x27;
    dev->cols = cols ? cols : 16;
    dev->rows = rows ? rows : 2;
    dev->backlight = 1;

    volt_delay_ms(50);

    // Init in 4-bit mode
    lcd_write4(dev, 0x30); volt_delay_ms(5);
    lcd_write4(dev, 0x30); volt_delay_ms(1);
    lcd_write4(dev, 0x30); volt_delay_ms(1);
    lcd_write4(dev, 0x20); volt_delay_ms(1);

    // Function set: 4-bit, 2 lines, 5x8 font
    lcd_command(dev, 0x28);
    // Display on, cursor off, blink off
    lcd_command(dev, 0x0C);
    // Clear display
    lcd_command(dev, 0x01);
    volt_delay_ms(2);
    // Entry mode: left to right
    lcd_command(dev, 0x06);

    return VOLT_OK;
}

void hd44780_clear(hd44780_t *dev)
{
    lcd_command(dev, 0x01);
    volt_delay_ms(2);
}

void hd44780_set_cursor(hd44780_t *dev, uint8_t col, uint8_t row)
{
    uint8_t offsets[] = { 0x00, 0x40, 0x14, 0x54 };
    if (row >= dev->rows) row = dev->rows - 1;
    lcd_command(dev, 0x80 | (col + offsets[row]));
}

void hd44780_print(hd44780_t *dev, const char *text)
{
    while (*text) {
        lcd_send(dev, *text++, LCD_RS);
    }
}

void hd44780_backlight(hd44780_t *dev, uint8_t on)
{
    dev->backlight = on;
    uint8_t val = on ? LCD_BL : 0;
    volt_i2c_write(dev->bus_id, dev->addr, &val, 1);
}

void hd44780_create_char(hd44780_t *dev, uint8_t slot,
                         uint8_t r0, uint8_t r1, uint8_t r2, uint8_t r3,
                         uint8_t r4, uint8_t r5, uint8_t r6, uint8_t r7)
{
    uint8_t rows[8] = { r0, r1, r2, r3, r4, r5, r6, r7 };
    lcd_command(dev, 0x40 | ((slot & 0x07) << 3));
    for (int i = 0; i < 8; i++) lcd_send(dev, rows[i] & 0x1F, LCD_RS);
    lcd_command(dev, 0x80); // back to DDRAM; caller sets the cursor
}

void hd44780_write_char(hd44780_t *dev, uint8_t code)
{
    lcd_send(dev, code, LCD_RS);
}

// Re-run the init sequence (same address/size, backlight kept). Recovers a
// display whose 4-bit nibble sync was lost to noise or a supply dip.
void hd44780_reinit(hd44780_t *dev)
{
    uint8_t bl = dev->backlight;
    hd44780_init(dev, dev->bus_id, dev->addr, dev->cols, dev->rows);
    hd44780_backlight(dev, bl);
}
