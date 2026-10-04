/* HTU21D Temperature + Humidity sensor (I2C addr: 0x40) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/task.h"

volt_error_t htu21d_init(htu21d_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x40;
    // Soft reset
    uint8_t cmd = 0xFE;
    volt_i2c_write(bus_id, dev->addr, &cmd, 1);
    volt_delay_ms(15);
    return VOLT_OK;
}

float htu21d_temperature(htu21d_t *dev)
{
    uint8_t cmd = 0xE3; // trigger temp, hold master
    uint8_t data[3];
    volt_i2c_write(dev->bus_id, dev->addr, &cmd, 1);
    volt_delay_ms(50);
    volt_i2c_read(dev->bus_id, dev->addr, data, 3);
    uint16_t raw = ((uint16_t)data[0] << 8) | (data[1] & 0xFC);
    return -46.85f + 175.72f * (float)raw / 65536.0f;
}

float htu21d_humidity(htu21d_t *dev)
{
    uint8_t cmd = 0xE5; // trigger humidity, hold master
    uint8_t data[3];
    volt_i2c_write(dev->bus_id, dev->addr, &cmd, 1);
    volt_delay_ms(50);
    volt_i2c_read(dev->bus_id, dev->addr, data, 3);
    uint16_t raw = ((uint16_t)data[0] << 8) | (data[1] & 0xFC);
    return -6.0f + 125.0f * (float)raw / 65536.0f;
}
