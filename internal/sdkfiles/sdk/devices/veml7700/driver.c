/* VEML7700 High-accuracy Ambient Light Sensor driver (I2C address: 0x10) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"

#define VEML7700_REG_CONF  0x00
#define VEML7700_REG_ALS   0x04
#define VEML7700_REG_WHITE 0x05

volt_error_t veml7700_init(veml7700_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x10;

    // Config: gain ×1, integration time 100ms, power on
    uint8_t cmd[3] = { VEML7700_REG_CONF, 0x00, 0x00 };
    volt_i2c_write(bus_id, dev->addr, cmd, 3);

    log_info("veml7700", "initialized at bus %d addr 0x%02X", bus_id, dev->addr);
    return VOLT_OK;
}

float veml7700_lux(veml7700_t *dev)
{
    uint16_t raw = volt_i2c_read_reg16(dev->bus_id, dev->addr, VEML7700_REG_ALS);
    // Resolution at gain ×1, IT 100ms = 0.0576 lux/count
    float lux = (float)raw * 0.0576f;

    // Non-linear correction for high lux values
    if (lux > 1000.0f) {
        lux = 6.0135e-13f * lux * lux * lux * lux
            - 9.3924e-9f  * lux * lux * lux
            + 8.1488e-5f  * lux * lux
            + 1.0023f     * lux;
    }
    return lux;
}

float veml7700_white(veml7700_t *dev)
{
    uint16_t raw = volt_i2c_read_reg16(dev->bus_id, dev->addr, VEML7700_REG_WHITE);
    return (float)raw * 0.0576f;
}
