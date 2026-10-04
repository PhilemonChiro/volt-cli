/* ADS1115 16-bit 4-channel ADC (I2C addr: 0x48-0x4B) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include "volt/task.h"

#define ADS1115_REG_CONVERSION 0x00
#define ADS1115_REG_CONFIG     0x01

volt_error_t ads1115_init(ads1115_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x48;
    dev->gain_voltage = 4.096f; // default ±4.096V (PGA = 001)
    log_info("ads1115", "initialized at 0x%02X", dev->addr);
    return VOLT_OK;
}

int ads1115_read_raw(ads1115_t *dev, uint8_t channel)
{
    if (channel > 3) return 0;

    // Config: single-shot, channel, ±4.096V gain, 128SPS, comparator disabled
    uint16_t config = 0x8000  // OS: start single conversion
        | ((0x04 + channel) << 12)  // MUX: AINx vs GND
        | (0x01 << 9)   // PGA: ±4.096V
        | (0x01 << 8)   // MODE: single-shot
        | (0x04 << 5)   // DR: 128 SPS
        | 0x03;          // COMP: disabled

    uint8_t cmd[3] = { ADS1115_REG_CONFIG, config >> 8, config & 0xFF };
    volt_i2c_write(dev->bus_id, dev->addr, cmd, 3);

    // Wait for conversion (~8ms at 128SPS)
    volt_delay_ms(10);

    return (int16_t)volt_i2c_read_reg16(dev->bus_id, dev->addr, ADS1115_REG_CONVERSION);
}

float ads1115_voltage(ads1115_t *dev, uint8_t channel)
{
    int raw = ads1115_read_raw(dev, channel);
    return (float)raw * dev->gain_voltage / 32768.0f;
}
