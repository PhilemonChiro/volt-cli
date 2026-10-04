/* MPU6050 6-axis IMU driver (I2C address: 0x68) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include "volt/task.h"  // for volt_tick_ms()

#define MPU6050_REG_PWR_MGMT_1  0x6B
#define MPU6050_REG_ACCEL_CFG   0x1C
#define MPU6050_REG_GYRO_CFG    0x1B
#define MPU6050_REG_ACCEL_XOUT  0x3B
#define MPU6050_REG_WHO_AM_I    0x75

volt_error_t mpu6050_init(mpu6050_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x68;

    // Verify chip ID
    uint8_t who = volt_i2c_read_reg(bus_id, dev->addr, MPU6050_REG_WHO_AM_I);
    if (who != 0x68) {
        log_error("mpu6050", "wrong WHO_AM_I: 0x%02X (expected 0x68)", who);
        return VOLT_ERR_INTERNAL;
    }

    // Wake up (clear SLEEP bit)
    volt_i2c_write_reg(bus_id, dev->addr, MPU6050_REG_PWR_MGMT_1, 0x00);
    // Accel range ±2g
    volt_i2c_write_reg(bus_id, dev->addr, MPU6050_REG_ACCEL_CFG, 0x00);
    // Gyro range ±250°/s
    volt_i2c_write_reg(bus_id, dev->addr, MPU6050_REG_GYRO_CFG, 0x00);

    log_info("mpu6050", "initialized at bus %d addr 0x%02X", bus_id, dev->addr);
    return VOLT_OK;
}

static void mpu6050_read_all(mpu6050_t *dev)
{
    uint8_t data[14];
    uint8_t reg = MPU6050_REG_ACCEL_XOUT;
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, data, 14);

    // Raw values (signed 16-bit, big-endian)
    int16_t ax = (int16_t)((data[0] << 8) | data[1]);
    int16_t ay = (int16_t)((data[2] << 8) | data[3]);
    int16_t az = (int16_t)((data[4] << 8) | data[5]);
    int16_t temp_raw = (int16_t)((data[6] << 8) | data[7]);
    int16_t gx = (int16_t)((data[8] << 8) | data[9]);
    int16_t gy = (int16_t)((data[10] << 8) | data[11]);
    int16_t gz = (int16_t)((data[12] << 8) | data[13]);

    // Convert to physical units (±2g = /16384, ±250°/s = /131)
    dev->accel_x = ax / 16384.0f;
    dev->accel_y = ay / 16384.0f;
    dev->accel_z = az / 16384.0f;
    dev->gyro_x  = gx / 131.0f;
    dev->gyro_y  = gy / 131.0f;
    dev->gyro_z  = gz / 131.0f;
    dev->temperature = temp_raw / 340.0f + 36.53f;
}

// Cache: only re-read if >2ms since last read (consistent snapshot within a cycle)
static uint32_t last_read_ms = 0;
static void mpu6050_read_cached(mpu6050_t *dev)
{
    uint32_t now = volt_tick_ms();
    if (now - last_read_ms >= 2) {
        mpu6050_read_all(dev);
        last_read_ms = now;
    }
}

float mpu6050_accel_x(mpu6050_t *dev)     { mpu6050_read_cached(dev); return dev->accel_x; }
float mpu6050_accel_y(mpu6050_t *dev)     { mpu6050_read_cached(dev); return dev->accel_y; }
float mpu6050_accel_z(mpu6050_t *dev)     { mpu6050_read_cached(dev); return dev->accel_z; }
float mpu6050_gyro_x(mpu6050_t *dev)      { mpu6050_read_cached(dev); return dev->gyro_x; }
float mpu6050_gyro_y(mpu6050_t *dev)      { mpu6050_read_cached(dev); return dev->gyro_y; }
float mpu6050_gyro_z(mpu6050_t *dev)      { mpu6050_read_cached(dev); return dev->gyro_z; }
float mpu6050_temperature(mpu6050_t *dev) { mpu6050_read_cached(dev); return dev->temperature; }
