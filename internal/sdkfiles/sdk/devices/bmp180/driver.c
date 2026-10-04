/* BMP180 Barometric Pressure + Temperature (I2C addr: 0x77) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include "volt/task.h"

volt_error_t bmp180_init(bmp180_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x77;

    uint8_t id = volt_i2c_read_reg(bus_id, dev->addr, 0xD0);
    if (id != 0x55) {
        log_error("bmp180", "wrong chip ID: 0x%02X (expected 0x55)", id);
        return VOLT_ERR_INTERNAL;
    }

    // Read calibration (11 x 16-bit values from 0xAA-0xBF)
    dev->AC1 = (int16_t)volt_i2c_read_reg16(bus_id, dev->addr, 0xAA);
    dev->AC2 = (int16_t)volt_i2c_read_reg16(bus_id, dev->addr, 0xAC);
    dev->AC3 = (int16_t)volt_i2c_read_reg16(bus_id, dev->addr, 0xAE);
    dev->AC4 = volt_i2c_read_reg16(bus_id, dev->addr, 0xB0);
    dev->AC5 = volt_i2c_read_reg16(bus_id, dev->addr, 0xB2);
    dev->AC6 = volt_i2c_read_reg16(bus_id, dev->addr, 0xB4);
    dev->B1  = (int16_t)volt_i2c_read_reg16(bus_id, dev->addr, 0xB6);
    dev->B2  = (int16_t)volt_i2c_read_reg16(bus_id, dev->addr, 0xB8);
    dev->MB  = (int16_t)volt_i2c_read_reg16(bus_id, dev->addr, 0xBA);
    dev->MC  = (int16_t)volt_i2c_read_reg16(bus_id, dev->addr, 0xBC);
    dev->MD  = (int16_t)volt_i2c_read_reg16(bus_id, dev->addr, 0xBE);

    log_info("bmp180", "initialized at 0x%02X", dev->addr);
    return VOLT_OK;
}

float bmp180_temperature(bmp180_t *dev)
{
    volt_i2c_write_reg(dev->bus_id, dev->addr, 0xF4, 0x2E);
    volt_delay_ms(5);
    int32_t UT = (int32_t)volt_i2c_read_reg16(dev->bus_id, dev->addr, 0xF6);

    int32_t X1 = (UT - (int32_t)dev->AC6) * (int32_t)dev->AC5 / 32768;
    int32_t X2 = ((int32_t)dev->MC * 2048) / (X1 + (int32_t)dev->MD);
    dev->B5 = X1 + X2;
    return (float)((dev->B5 + 8) / 16) / 10.0f;
}

float bmp180_pressure(bmp180_t *dev)
{
    bmp180_temperature(dev); // need B5

    volt_i2c_write_reg(dev->bus_id, dev->addr, 0xF4, 0x34);
    volt_delay_ms(5);
    int32_t UP = (int32_t)volt_i2c_read_reg16(dev->bus_id, dev->addr, 0xF6);

    int32_t B6 = dev->B5 - 4000;
    int32_t X1 = ((int32_t)dev->B2 * (B6 * B6 / 4096)) / 2048;
    int32_t X2 = (int32_t)dev->AC2 * B6 / 2048;
    int32_t X3 = X1 + X2;
    int32_t B3 = (((int32_t)dev->AC1 * 4 + X3) + 2) / 4;
    X1 = (int32_t)dev->AC3 * B6 / 8192;
    X2 = ((int32_t)dev->B1 * (B6 * B6 / 4096)) / 65536;
    X3 = ((X1 + X2) + 2) / 4;
    uint32_t B4 = (uint32_t)dev->AC4 * (uint32_t)(X3 + 32768) / 32768;
    uint32_t B7 = ((uint32_t)UP - B3) * 50000;
    int32_t p;
    if (B7 < 0x80000000) p = (B7 * 2) / B4;
    else p = (B7 / B4) * 2;
    X1 = (p / 256) * (p / 256);
    X1 = (X1 * 3038) / 65536;
    X2 = (-7357 * p) / 65536;
    p = p + (X1 + X2 + 3791) / 16;
    return (float)p / 100.0f; // hPa
}
