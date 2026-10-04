#include "volt/spi_devices.h"
#include "volt/sensor.h"
/* MAX7219 LED Dot Matrix / 7-Segment Driver (SPI, CS active low) */

#include "volt/spi.h"
#include "volt/log.h"

static void max7219_send(max7219_t *dev, uint8_t reg, uint8_t data)
{
    uint8_t buf[2] = { reg, data };
    volt_spi_cs_select(dev->cs_pin);
    volt_spi_write(dev->bus_id, buf, 2);
    volt_spi_cs_deselect(dev->cs_pin);
}

volt_error_t max7219_init(max7219_t *dev, uint8_t bus_id, uint8_t cs_pin)
{
    dev->bus_id = bus_id;
    dev->cs_pin = cs_pin;
    dev->num_devices = 1;
    volt_spi_cs_deselect(cs_pin);

    max7219_send(dev, 0x09, 0x00); // decode mode: none (raw segments)
    max7219_send(dev, 0x0A, 0x08); // intensity: 50%
    max7219_send(dev, 0x0B, 0x07); // scan limit: all 8 digits/rows
    max7219_send(dev, 0x0C, 0x01); // shutdown: normal operation
    max7219_send(dev, 0x0F, 0x00); // display test: off

    // Clear all rows
    for (int i = 1; i <= 8; i++) {
        max7219_send(dev, i, 0x00);
    }

    log_info("max7219", "LED driver on bus %d cs=%d", bus_id, cs_pin);
    return VOLT_OK;
}

void max7219_set_row(max7219_t *dev, uint8_t row, uint8_t data)
{
    if (row > 7) return;
    max7219_send(dev, row + 1, data);
}

void max7219_set_intensity(max7219_t *dev, uint8_t level)
{
    if (level > 15) level = 15;
    max7219_send(dev, 0x0A, level);
}

void max7219_clear(max7219_t *dev)
{
    for (int i = 1; i <= 8; i++) {
        max7219_send(dev, i, 0x00);
    }
}

void max7219_set_digit(max7219_t *dev, uint8_t pos, uint8_t value, uint8_t dp)
{
    // 7-segment encoding
    static const uint8_t segments[] = {
        0x7E, 0x30, 0x6D, 0x79, 0x33, // 0-4
        0x5B, 0x5F, 0x70, 0x7F, 0x7B, // 5-9
    };
    uint8_t seg = (value < 10) ? segments[value] : 0x00;
    if (dp) seg |= 0x80;
    max7219_send(dev, pos + 1, seg);
}
