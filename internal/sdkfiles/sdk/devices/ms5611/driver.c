/* MS5611 High-resolution Barometric Pressure (I2C addr: 0x76/0x77) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/task.h"
#include "volt/log.h"

volt_error_t ms5611_init(ms5611_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x77;

    // Reset
    uint8_t cmd = 0x1E;
    volt_i2c_write(bus_id, dev->addr, &cmd, 1);
    volt_delay_ms(10);

    // Read PROM calibration (C1-C6 at 0xA2-0xAC)
    for (int i = 1; i <= 6; i++) {
        dev->C[i] = volt_i2c_read_reg16(bus_id, dev->addr, 0xA0 + i * 2);
    }

    log_info("ms5611", "barometer at 0x%02X", dev->addr);
    return VOLT_OK;
}

static uint32_t ms5611_read_adc(ms5611_t *dev, uint8_t cmd)
{
    volt_i2c_write(dev->bus_id, dev->addr, &cmd, 1);
    volt_delay_ms(10); // OSR 4096
    uint8_t data[3];
    uint8_t reg = 0x00;
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, data, 3);
    return ((uint32_t)data[0] << 16) | ((uint32_t)data[1] << 8) | data[2];
}

float ms5611_temperature(ms5611_t *dev)
{
    uint32_t D2 = ms5611_read_adc(dev, 0x58); // temp, OSR 4096
    dev->dT = D2 - ((int32_t)dev->C[5] << 8);
    dev->TEMP = 2000 + ((int64_t)dev->dT * dev->C[6]) / 8388608;
    return dev->TEMP / 100.0f;
}

float ms5611_pressure(ms5611_t *dev)
{
    ms5611_temperature(dev); // need dT and TEMP

    uint32_t D1 = ms5611_read_adc(dev, 0x48); // pressure, OSR 4096
    int64_t OFF  = ((int64_t)dev->C[2] << 16) + ((int64_t)dev->C[4] * dev->dT) / 128;
    int64_t SENS = ((int64_t)dev->C[1] << 15) + ((int64_t)dev->C[3] * dev->dT) / 256;

    // Second order compensation for low temp
    if (dev->TEMP < 2000) {
        int64_t T2 = ((int64_t)dev->dT * dev->dT) / 2147483648LL;
        int64_t OFF2 = 5 * ((dev->TEMP - 2000) * (dev->TEMP - 2000)) / 2;
        int64_t SENS2 = 5 * ((dev->TEMP - 2000) * (dev->TEMP - 2000)) / 4;
        OFF -= OFF2;
        SENS -= SENS2;
    }

    int32_t P = (D1 * SENS / 2097152 - OFF) / 32768;
    return P / 100.0f; // mbar/hPa
}
