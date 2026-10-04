/* INA219 Current/Voltage/Power monitor driver (I2C address: 0x40) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"

#define INA219_REG_CONFIG    0x00
#define INA219_REG_SHUNT_V   0x01
#define INA219_REG_BUS_V     0x02
#define INA219_REG_POWER     0x03
#define INA219_REG_CURRENT   0x04
#define INA219_REG_CALIB     0x05

volt_error_t ina219_init(ina219_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x40;

    // Config: 32V bus range, ±320mV gain, 12-bit ADC, continuous shunt+bus
    uint16_t config = 0x399F;
    uint8_t cfg[3] = { INA219_REG_CONFIG, config >> 8, config & 0xFF };
    volt_i2c_write(bus_id, dev->addr, cfg, 3);

    // Calibration for 0.1Ω shunt, 3.2A max → Cal = 4096
    dev->current_lsb = 0.0001f;  // 100 µA per bit
    uint16_t cal = 4096;
    uint8_t cal_cmd[3] = { INA219_REG_CALIB, cal >> 8, cal & 0xFF };
    volt_i2c_write(bus_id, dev->addr, cal_cmd, 3);

    log_info("ina219", "initialized at bus %d addr 0x%02X (0.1ohm shunt)", bus_id, dev->addr);
    return VOLT_OK;
}

float ina219_voltage(ina219_t *dev)
{
    uint16_t raw = volt_i2c_read_reg16(dev->bus_id, dev->addr, INA219_REG_BUS_V);
    return (float)(raw >> 3) * 0.004f;  // shift 3, × 4mV
}

float ina219_current(ina219_t *dev)
{
    int16_t raw = (int16_t)volt_i2c_read_reg16(dev->bus_id, dev->addr, INA219_REG_CURRENT);
    return (float)raw * dev->current_lsb;  // Amps
}

float ina219_power(ina219_t *dev)
{
    int16_t raw = (int16_t)volt_i2c_read_reg16(dev->bus_id, dev->addr, INA219_REG_POWER);
    return (float)raw * dev->current_lsb * 20.0f;  // Watts (power LSB = 20 × current LSB)
}

float ina219_shunt_voltage(ina219_t *dev)
{
    int16_t raw = (int16_t)volt_i2c_read_reg16(dev->bus_id, dev->addr, INA219_REG_SHUNT_V);
    return (float)raw * 0.00001f;  // 10 µV per LSB → Volts
}
