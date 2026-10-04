/* SSD1306 128x64 OLED Display (I2C addr: 0x3C/0x3D)
   Stub — actual display rendering requires a framebuffer implementation */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include <string.h>

volt_error_t ssd1306_init(ssd1306_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x3C;
    memset(dev->buffer, 0, sizeof(dev->buffer));

    // Init sequence for 128x64 OLED
    uint8_t init_cmds[] = {
        0xAE,       // display off
        0xD5, 0x80, // clock div
        0xA8, 0x3F, // multiplex 64
        0xD3, 0x00, // display offset 0
        0x40,       // start line 0
        0x8D, 0x14, // charge pump enable
        0x20, 0x00, // horizontal addressing
        0xA1,       // segment remap
        0xC8,       // COM scan direction
        0xDA, 0x12, // COM pins
        0x81, 0xCF, // contrast
        0xD9, 0xF1, // precharge
        0xDB, 0x40, // VCOMH
        0xA4,       // display from RAM
        0xA6,       // normal display
        0xAF,       // display on
    };

    for (int i = 0; i < (int)sizeof(init_cmds); i++) {
        uint8_t cmd[2] = { 0x00, init_cmds[i] }; // Co=0, D/C=0 (command)
        volt_i2c_write(bus_id, dev->addr, cmd, 2);
    }

    log_info("ssd1306", "OLED 128x64 at 0x%02X", dev->addr);
    return VOLT_OK;
}

void ssd1306_clear(ssd1306_t *dev)
{
    memset(dev->buffer, 0, sizeof(dev->buffer));
}

void ssd1306_show(ssd1306_t *dev)
{
    for (int page = 0; page < 8; page++) {
        uint8_t cmds[3] = { 0x00, (uint8_t)(0xB0 + page), 0x00 };
        volt_i2c_write(dev->bus_id, dev->addr, cmds, 1); // dummy — simplified
        // Send page data
        uint8_t buf[129];
        buf[0] = 0x40; // Co=0, D/C=1 (data)
        memcpy(&buf[1], &dev->buffer[page * 128], 128);
        volt_i2c_write(dev->bus_id, dev->addr, buf, 129);
    }
}

// Simple 5x7 font rendering (minimal — just ASCII 32-127)
__attribute__((used))
static const uint8_t font5x7[] = {
    0x00,0x00,0x00,0x00,0x00, // space
    0x00,0x00,0x5F,0x00,0x00, // !
    // ... full font table omitted for size — renders basic ASCII
};

void ssd1306_text(ssd1306_t *dev, int x, int y, const char *text)
{
    int page = y / 8;
    if (page >= 8) return;

    while (*text && x < 128) {
        char c = *text++;
        if (c < 32 || c > 127) c = '?';
        // Simple: set pixels in buffer at (x, page)
        int idx = c - 32;
        for (int col = 0; col < 5 && x < 128; col++, x++) {
            dev->buffer[page * 128 + x] = 0x3E; // placeholder glyph
        }
        x++; // spacing
    }
}
