/* QMC5883L Digital Compass / Magnetometer (I2C addr: 0x0D) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include "volt/task.h"
#include <math.h>

volt_error_t qmc5883l_init(qmc5883l_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x0D;

    // Soft reset
    volt_i2c_write_reg(bus_id, dev->addr, 0x0B, 0x01);
    volt_delay_ms(10);
    // Continuous mode, 200Hz, 8 Gauss range, 512 oversampling
    volt_i2c_write_reg(bus_id, dev->addr, 0x09, 0x1D);

    log_info("qmc5883l", "compass at 0x%02X", dev->addr);
    return VOLT_OK;
}

static void qmc5883l_read_raw(qmc5883l_t *dev)
{
    uint8_t data[6];
    uint8_t reg = 0x00;
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, data, 6);

    int16_t rx = (int16_t)((data[1] << 8) | data[0]);
    int16_t ry = (int16_t)((data[3] << 8) | data[2]);
    int16_t rz = (int16_t)((data[5] << 8) | data[4]);

    // 8 Gauss range: 3000 LSB/Gauss
    dev->x = rx / 3000.0f;
    dev->y = ry / 3000.0f;
    dev->z = rz / 3000.0f;
}

float qmc5883l_x(qmc5883l_t *dev) { qmc5883l_read_raw(dev); return dev->x; }
float qmc5883l_y(qmc5883l_t *dev) { return dev->y; }
float qmc5883l_z(qmc5883l_t *dev) { return dev->z; }

float qmc5883l_heading(qmc5883l_t *dev)
{
    qmc5883l_read_raw(dev);
    float heading = atan2f(dev->y, dev->x) * 180.0f / 3.14159265f;
    if (heading < 0) heading += 360.0f;
    return heading;
}
