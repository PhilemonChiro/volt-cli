#include "volt/spi_devices.h"
/* MCP3008 8-Channel 10-bit ADC
   SPI Mode 0, max 3.6MHz at 5V, CS active low */

#include "volt/spi.h"
#include "volt/log.h"

volt_error_t mcp3008_init(mcp3008_t *dev, uint8_t bus_id, uint8_t cs_pin)
{
    dev->bus_id = bus_id;
    dev->cs_pin = cs_pin;
    volt_spi_cs_deselect(cs_pin);
    log_info("mcp3008", "8-channel ADC initialized on bus %d cs=%d", bus_id, cs_pin);
    return VOLT_OK;
}

int mcp3008_read(mcp3008_t *dev, uint8_t channel)
{
    if (channel > 7) return 0;

    // 3-byte transaction:
    // TX: [0x01, 0x80 | (channel << 4), 0x00]
    // RX: [xx, (result >> 8) & 0x03, result & 0xFF]
    uint8_t tx[3] = { 0x01, (uint8_t)(0x80 | (channel << 4)), 0x00 };
    uint8_t rx[3] = {0};

    volt_spi_cs_select(dev->cs_pin);
    volt_spi_transfer(dev->bus_id, tx, rx, 3);
    volt_spi_cs_deselect(dev->cs_pin);

    // 10-bit result from rx[1] and rx[2]
    return ((rx[1] & 0x03) << 8) | rx[2];
}
