/* BME680 Gas + Temp + Humidity + Pressure (I2C addr: 0x76/0x77)
   Air quality index sensor */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include "volt/task.h"

volt_error_t bme680_init(bme680_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x76;

    uint8_t id = volt_i2c_read_reg(bus_id, dev->addr, 0xD0);
    if (id != 0x61) {
        log_error("bme680", "wrong ID: 0x%02X (expected 0x61)", id);
        return VOLT_ERR_INTERNAL;
    }

    // Soft reset
    volt_i2c_write_reg(bus_id, dev->addr, 0xE0, 0xB6);
    volt_delay_ms(10);

    // Humidity oversampling x2
    volt_i2c_write_reg(bus_id, dev->addr, 0x72, 0x02);
    // Temp oversampling x8, Pressure oversampling x4
    volt_i2c_write_reg(bus_id, dev->addr, 0x74, 0x54);
    // Gas heater: 320°C, 150ms
    volt_i2c_write_reg(bus_id, dev->addr, 0x5A, 0x73); // heater temp
    volt_i2c_write_reg(bus_id, dev->addr, 0x64, 0x65); // heater duration
    // Enable gas measurement
    volt_i2c_write_reg(bus_id, dev->addr, 0x71, 0x10);

    log_info("bme680", "gas sensor initialized at 0x%02X", dev->addr);
    return VOLT_OK;
}

static void bme680_trigger_and_read(bme680_t *dev)
{
    // Trigger forced measurement
    uint8_t ctrl = volt_i2c_read_reg(dev->bus_id, dev->addr, 0x74);
    volt_i2c_write_reg(dev->bus_id, dev->addr, 0x74, ctrl | 0x01);
    volt_delay_ms(200);

    // Read raw temp (20-bit from 0x22-0x24)
    uint8_t data[8];
    uint8_t reg = 0x1F;
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, data, 8);

    uint32_t pres_raw = ((uint32_t)data[0] << 12) | ((uint32_t)data[1] << 4) | (data[2] >> 4);
    uint32_t temp_raw  = ((uint32_t)data[3] << 12) | ((uint32_t)data[4] << 4) | (data[5] >> 4);
    uint16_t hum_raw   = ((uint16_t)data[6] << 8) | data[7];

    // Simplified conversion (approximate, without full calibration)
    dev->last_temp = (float)temp_raw / 16384.0f * 100.0f - 40.0f;
    dev->last_hum  = (float)hum_raw / 65536.0f * 100.0f;
    dev->last_pres = (float)pres_raw / 256.0f;

    // Gas resistance (simplified)
    uint8_t gas_data[2];
    reg = 0x2A;
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, gas_data, 2);
    dev->last_gas = ((uint32_t)(gas_data[0] & 0x3F) << 2) | (gas_data[1] >> 6);
}

float bme680_temperature(bme680_t *dev) { bme680_trigger_and_read(dev); return dev->last_temp; }
float bme680_humidity(bme680_t *dev)    { return dev->last_hum; }
float bme680_pressure(bme680_t *dev)    { return dev->last_pres; }
int   bme680_gas(bme680_t *dev)         { return (int)dev->last_gas; }
