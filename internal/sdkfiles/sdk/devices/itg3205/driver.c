/* ITG3205 3-axis gyroscope (I2C addr: 0x68) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include <math.h>

#define ITG3205_REG_WHO_AM_I 0x00
#define ITG3205_REG_SMPLRT   0x15
#define ITG3205_REG_DLPF_FS  0x16
#define ITG3205_REG_GYRO_X   0x1D
#define ITG3205_REG_PWR_MGM  0x3E

volt_error_t itg3205_init(itg3205_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x68;

    uint8_t who = volt_i2c_read_reg(bus_id, dev->addr, ITG3205_REG_WHO_AM_I);
    if ((who & 0x7E) != 0x68) {
        log_error("itg3205", "wrong WHO_AM_I: 0x%02X (expected 0x68/0x69)", who);
        return VOLT_ERR_INTERNAL;
    }
    volt_i2c_write_reg(bus_id, dev->addr, ITG3205_REG_DLPF_FS, 0x18 | 0x03); // FS_SEL=3 (required), 42 Hz LPF
    volt_i2c_write_reg(bus_id, dev->addr, ITG3205_REG_SMPLRT, 9);            // 1 kHz / 10 = 100 Hz
    volt_i2c_write_reg(bus_id, dev->addr, ITG3205_REG_PWR_MGM, 0x01);        // clock from X gyro

    log_info("itg3205", "initialized at bus %d addr 0x%02X", bus_id, dev->addr);
    return VOLT_OK;
}

static void itg3205_read(itg3205_t *dev)
{
    uint8_t d[6] = {0};
    uint8_t reg = ITG3205_REG_GYRO_X;
    if (volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, d, 6) != VOLT_OK) return;  // keep last good sample
    // big-endian, 14.375 LSB per °/s
    dev->x = (int16_t)((d[0] << 8) | d[1]) / 14.375f;
    dev->y = (int16_t)((d[2] << 8) | d[3]) / 14.375f;
    dev->z = (int16_t)((d[4] << 8) | d[5]) / 14.375f;
}

float itg3205_x(itg3205_t *dev) { itg3205_read(dev); return dev->x; }
float itg3205_y(itg3205_t *dev) { itg3205_read(dev); return dev->y; }
float itg3205_z(itg3205_t *dev) { itg3205_read(dev); return dev->z; }
float itg3205_magnitude(itg3205_t *dev)
{
    itg3205_read(dev);
    return sqrtf(dev->x * dev->x + dev->y * dev->y + dev->z * dev->z);
}
