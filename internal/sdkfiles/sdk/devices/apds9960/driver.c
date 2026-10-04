/* APDS9960 Gesture/Proximity/RGB Color sensor (I2C addr: 0x39) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include "volt/task.h"

volt_error_t apds9960_init(apds9960_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x39;

    uint8_t id = volt_i2c_read_reg(bus_id, dev->addr, 0x92);
    if (id != 0xAB && id != 0x9C) {
        log_error("apds9960", "wrong ID: 0x%02X", id);
        return VOLT_ERR_INTERNAL;
    }

    // Enable power + proximity + ALS
    volt_i2c_write_reg(bus_id, dev->addr, 0x80, 0x07);
    // ALS integration time 100ms
    volt_i2c_write_reg(bus_id, dev->addr, 0x81, 0xDB);
    // Proximity gain 4x
    volt_i2c_write_reg(bus_id, dev->addr, 0x8F, 0x04);

    log_info("apds9960", "gesture/proximity sensor at 0x%02X", dev->addr);
    return VOLT_OK;
}

int apds9960_proximity(apds9960_t *dev)
{
    return volt_i2c_read_reg(dev->bus_id, dev->addr, 0x9C);
}

int apds9960_red(apds9960_t *dev)   { return volt_i2c_read_reg16(dev->bus_id, dev->addr, 0x96); }
int apds9960_green(apds9960_t *dev) { return volt_i2c_read_reg16(dev->bus_id, dev->addr, 0x98); }
int apds9960_blue(apds9960_t *dev)  { return volt_i2c_read_reg16(dev->bus_id, dev->addr, 0x9A); }
int apds9960_clear(apds9960_t *dev) { return volt_i2c_read_reg16(dev->bus_id, dev->addr, 0x94); }
