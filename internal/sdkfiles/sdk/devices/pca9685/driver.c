/* PCA9685 16-channel PWM/Servo controller (I2C addr: 0x40-0x7F) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include "volt/task.h"

#define PCA9685_MODE1       0x00
#define PCA9685_PRESCALE    0xFE
#define PCA9685_LED0_ON_L   0x06

volt_error_t pca9685_init(pca9685_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x40;
    dev->frequency = 50.0f; // default 50Hz for servos

    // Reset
    volt_i2c_write_reg(bus_id, dev->addr, PCA9685_MODE1, 0x00);
    volt_delay_ms(5);

    // Set frequency to 50Hz (servo default)
    // prescale = round(25MHz / (4096 * freq)) - 1
    uint8_t prescale = (uint8_t)(25000000.0f / (4096.0f * 50.0f) - 1.0f);

    // Must sleep to set prescale
    volt_i2c_write_reg(bus_id, dev->addr, PCA9685_MODE1, 0x10); // sleep
    volt_i2c_write_reg(bus_id, dev->addr, PCA9685_PRESCALE, prescale);
    volt_i2c_write_reg(bus_id, dev->addr, PCA9685_MODE1, 0x00); // wake
    volt_delay_ms(5);
    volt_i2c_write_reg(bus_id, dev->addr, PCA9685_MODE1, 0xA0); // auto-increment + restart

    log_info("pca9685", "initialized at 0x%02X (50Hz, 16 channels)", dev->addr);
    return VOLT_OK;
}

void pca9685_set_pwm(pca9685_t *dev, uint8_t channel, uint16_t on, uint16_t off)
{
    if (channel > 15) return;
    uint8_t reg = PCA9685_LED0_ON_L + 4 * channel;
    uint8_t data[5] = { reg, on & 0xFF, on >> 8, off & 0xFF, off >> 8 };
    volt_i2c_write(dev->bus_id, dev->addr, data, 5);
}

void pca9685_set_servo(pca9685_t *dev, uint8_t channel, int angle)
{
    // Map 0-180° to ~150-600 (1ms-2ms pulse at 50Hz, 4096 steps)
    if (angle < 0) angle = 0;
    if (angle > 180) angle = 180;
    uint16_t pulse = (uint16_t)(150 + (angle * 450) / 180);
    pca9685_set_pwm(dev, channel, 0, pulse);
}

void pca9685_set_duty(pca9685_t *dev, uint8_t channel, uint8_t duty)
{
    // Map 0-255 to 0-4095
    uint16_t off = (uint16_t)(duty * 4095 / 255);
    pca9685_set_pwm(dev, channel, 0, off);
}
