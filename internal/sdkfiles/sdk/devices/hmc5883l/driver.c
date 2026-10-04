/* HMC5883L 3-axis magnetometer (I2C addr: 0x1E) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include <math.h>

#define HMC5883L_REG_CONFIG_A 0x00
#define HMC5883L_REG_CONFIG_B 0x01
#define HMC5883L_REG_MODE     0x02
#define HMC5883L_REG_DATA_X   0x03
#define HMC5883L_REG_ID_A     0x0A

volt_error_t hmc5883l_init(hmc5883l_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x1E;

    uint8_t id = volt_i2c_read_reg(bus_id, dev->addr, HMC5883L_REG_ID_A);
    if (id != 'H') {
        log_error("hmc5883l", "wrong ID: 0x%02X (expected 'H') — QMC5883L clone at 0x0D?", id);
        return VOLT_ERR_INTERNAL;
    }
    volt_i2c_write_reg(bus_id, dev->addr, HMC5883L_REG_CONFIG_A, 0x70); // 8 samples avg, 15 Hz
    volt_i2c_write_reg(bus_id, dev->addr, HMC5883L_REG_CONFIG_B, 0x20); // gain ±1.3 Ga
    volt_i2c_write_reg(bus_id, dev->addr, HMC5883L_REG_MODE, 0x00);     // continuous

    log_info("hmc5883l", "initialized at bus %d addr 0x%02X", bus_id, dev->addr);
    return VOLT_OK;
}

static void hmc5883l_read(hmc5883l_t *dev)
{
    uint8_t d[6] = {0};
    uint8_t reg = HMC5883L_REG_DATA_X;
    if (volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, d, 6) != VOLT_OK) return;  // keep last good sample
    // register order is X, Z, Y — big-endian, 1090 LSB/gauss at ±1.3 Ga
    dev->x = (int16_t)((d[0] << 8) | d[1]) / 1090.0f;
    dev->z = (int16_t)((d[2] << 8) | d[3]) / 1090.0f;
    dev->y = (int16_t)((d[4] << 8) | d[5]) / 1090.0f;
}

float hmc5883l_x(hmc5883l_t *dev) { hmc5883l_read(dev); return dev->x; }
float hmc5883l_y(hmc5883l_t *dev) { hmc5883l_read(dev); return dev->y; }
float hmc5883l_z(hmc5883l_t *dev) { hmc5883l_read(dev); return dev->z; }
float hmc5883l_heading(hmc5883l_t *dev)
{
    hmc5883l_read(dev);
    float h = atan2f(dev->y, dev->x) * 180.0f / (float)M_PI;
    if (h < 0) h += 360.0f;
    return h;
}
