/* TCA9548A I2C Multiplexer — 8 channels (I2C addr: 0x70-0x77) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"

volt_error_t tca9548a_init(tca9548a_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x70;
    dev->current_channel = 0xFF;
    // Disable all channels
    uint8_t val = 0x00;
    volt_i2c_write(bus_id, dev->addr, &val, 1);
    log_info("tca9548a", "I2C mux at 0x%02X (8 channels)", dev->addr);
    return VOLT_OK;
}

void tca9548a_select(tca9548a_t *dev, uint8_t channel)
{
    if (channel > 7) return;
    uint8_t val = 1 << channel;
    volt_i2c_write(dev->bus_id, dev->addr, &val, 1);
    dev->current_channel = channel;
}

void tca9548a_disable(tca9548a_t *dev)
{
    uint8_t val = 0x00;
    volt_i2c_write(dev->bus_id, dev->addr, &val, 1);
    dev->current_channel = 0xFF;
}
