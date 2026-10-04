/* ADXL345 3-axis accelerometer (I2C addr: 0x53) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include <math.h>

#define ADXL345_REG_DEVID       0x00
#define ADXL345_REG_BW_RATE     0x2C
#define ADXL345_REG_POWER_CTL   0x2D
#define ADXL345_REG_DATA_FORMAT 0x31
#define ADXL345_REG_DATAX0      0x32

volt_error_t adxl345_init(adxl345_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x53;

    uint8_t id = volt_i2c_read_reg(bus_id, dev->addr, ADXL345_REG_DEVID);
    if (id != 0xE5) {
        log_error("adxl345", "wrong DEVID: 0x%02X (expected 0xE5)", id);
        return VOLT_ERR_INTERNAL;
    }
    volt_i2c_write_reg(bus_id, dev->addr, ADXL345_REG_DATA_FORMAT, 0x08); // full res, ±2g
    volt_i2c_write_reg(bus_id, dev->addr, ADXL345_REG_BW_RATE, 0x0A);     // 100 Hz
    volt_i2c_write_reg(bus_id, dev->addr, ADXL345_REG_POWER_CTL, 0x08);   // measure

    log_info("adxl345", "initialized at bus %d addr 0x%02X", bus_id, dev->addr);
    return VOLT_OK;
}

static void adxl345_read(adxl345_t *dev)
{
    uint8_t d[6] = {0};
    uint8_t reg = ADXL345_REG_DATAX0;
    if (volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, d, 6) != VOLT_OK) return;  // keep last good sample
    // little-endian, 3.9 mg/LSB in full-resolution mode
    dev->x = (int16_t)((d[1] << 8) | d[0]) * 0.0039f;
    dev->y = (int16_t)((d[3] << 8) | d[2]) * 0.0039f;
    dev->z = (int16_t)((d[5] << 8) | d[4]) * 0.0039f;
}

float adxl345_x(adxl345_t *dev) { adxl345_read(dev); return dev->x; }
float adxl345_y(adxl345_t *dev) { adxl345_read(dev); return dev->y; }
float adxl345_z(adxl345_t *dev) { adxl345_read(dev); return dev->z; }
float adxl345_magnitude(adxl345_t *dev)
{
    adxl345_read(dev);
    return sqrtf(dev->x * dev->x + dev->y * dev->y + dev->z * dev->z);
}
