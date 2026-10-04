#include "volt/spi_devices.h"
/* MAX31855 Thermocouple-to-Digital Converter
   SPI Mode 0, max 5MHz, CS active low, read-only */

#include "volt/spi.h"
#include "volt/log.h"

static void max31855_read_raw(max31855_t *dev)
{
    uint8_t data[4];
    volt_spi_cs_select(dev->cs_pin);
    volt_spi_read(dev->bus_id, data, 4);
    volt_spi_cs_deselect(dev->cs_pin);

    uint32_t raw = ((uint32_t)data[0] << 24) | ((uint32_t)data[1] << 16) |
                   ((uint32_t)data[2] << 8) | data[3];

    // Fault bit (bit 16)
    dev->fault = (raw >> 16) & 0x01;

    // Thermocouple temperature: bits [31:18], 14-bit signed, 0.25°C/LSB
    int16_t tc_raw = (int16_t)(raw >> 18);
    if (tc_raw & 0x2000) tc_raw |= 0xC000;  // sign extend
    dev->thermocouple_temp = tc_raw * 0.25f;

    // Internal temperature: bits [15:4], 12-bit signed, 0.0625°C/LSB
    int16_t int_raw = (int16_t)((raw >> 4) & 0x0FFF);
    if (int_raw & 0x0800) int_raw |= 0xF000;  // sign extend
    dev->internal_temp = int_raw * 0.0625f;
}

volt_error_t max31855_init(max31855_t *dev, uint8_t bus_id, uint8_t cs_pin)
{
    dev->bus_id = bus_id;
    dev->cs_pin = cs_pin;
    volt_spi_cs_deselect(cs_pin);

    // Test read
    max31855_read_raw(dev);
    log_info("max31855", "initialized: tc=%.1fC internal=%.1fC fault=%d",
             dev->thermocouple_temp, dev->internal_temp, dev->fault);
    return VOLT_OK;
}

float max31855_temperature(max31855_t *dev)
{
    max31855_read_raw(dev);
    return dev->thermocouple_temp;
}

float max31855_internal(max31855_t *dev)
{
    max31855_read_raw(dev);
    return dev->internal_temp;
}

int max31855_is_fault(max31855_t *dev)
{
    max31855_read_raw(dev);
    return dev->fault;
}
