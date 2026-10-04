/* PCF8574 I2C GPIO Expander (addr: 0x20-0x27)
   Used for I2C LCD modules (16x2 HD44780 via I2C backpack) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"

volt_error_t pcf8574_init(pcf8574_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x27;
    dev->state = 0xFF; // all pins high (input mode default)
    volt_i2c_write(bus_id, dev->addr, &dev->state, 1);
    log_info("pcf8574", "GPIO expander at 0x%02X", dev->addr);
    return VOLT_OK;
}

void pcf8574_write(pcf8574_t *dev, uint8_t value)
{
    dev->state = value;
    volt_i2c_write(dev->bus_id, dev->addr, &dev->state, 1);
}

uint8_t pcf8574_read(pcf8574_t *dev)
{
    uint8_t val = 0;
    volt_i2c_read(dev->bus_id, dev->addr, &val, 1);
    return val;
}

void pcf8574_set_pin(pcf8574_t *dev, uint8_t pin, uint8_t level)
{
    if (pin > 7) return;
    if (level) dev->state |= (1 << pin);
    else dev->state &= ~(1 << pin);
    volt_i2c_write(dev->bus_id, dev->addr, &dev->state, 1);
}
