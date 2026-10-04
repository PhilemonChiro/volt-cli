/* MCP4725 12-bit DAC (I2C addr: 0x60/0x61) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"

volt_error_t mcp4725_init(mcp4725_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x60;
    log_info("mcp4725", "12-bit DAC at 0x%02X", dev->addr);
    return VOLT_OK;
}

void mcp4725_write(mcp4725_t *dev, uint16_t value)
{
    if (value > 4095) value = 4095;
    // Fast mode write: [0x00 | (value >> 8), value & 0xFF]
    uint8_t data[2] = { (uint8_t)(value >> 8), (uint8_t)(value & 0xFF) };
    volt_i2c_write(dev->bus_id, dev->addr, data, 2);
}

void mcp4725_set_voltage(mcp4725_t *dev, float voltage, float vref)
{
    if (voltage < 0) voltage = 0;
    if (voltage > vref) voltage = vref;
    uint16_t value = (uint16_t)(voltage / vref * 4095.0f);
    mcp4725_write(dev, value);
}
