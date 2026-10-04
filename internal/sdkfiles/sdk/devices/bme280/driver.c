/* BME280 Temperature/Humidity/Pressure sensor driver
   Works on ESP32 and RP2040 — uses the portable volt_i2c_ functions */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"

#define BME280_REG_ID        0xD0
#define BME280_REG_CTRL_HUM  0xF2
#define BME280_REG_CTRL_MEAS 0xF4
#define BME280_REG_CONFIG    0xF5
#define BME280_REG_CALIB00   0x88
#define BME280_REG_CALIB26   0xE1
#define BME280_REG_DATA      0xF7

volt_error_t bme280_init(bme280_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr;

    // Check chip ID (should be 0x60 for BME280)
    uint8_t id = volt_i2c_read_reg(bus_id, addr, BME280_REG_ID);
    if (id != 0x60) {
        log_error("bme280", "wrong chip ID: 0x%02X (expected 0x60)", id);
        return VOLT_ERR_INTERNAL;
    }

    // Read calibration data (temperature + pressure: registers 0x88-0x9F)
    uint8_t calib[26];
    uint8_t reg = BME280_REG_CALIB00;
    volt_i2c_write_read(bus_id, addr, &reg, 1, calib, 26);

    dev->dig_T1 = (uint16_t)(calib[1] << 8 | calib[0]);
    dev->dig_T2 = (int16_t)(calib[3] << 8 | calib[2]);
    dev->dig_T3 = (int16_t)(calib[5] << 8 | calib[4]);
    dev->dig_P1 = (uint16_t)(calib[7] << 8 | calib[6]);
    dev->dig_P2 = (int16_t)(calib[9] << 8 | calib[8]);
    dev->dig_P3 = (int16_t)(calib[11] << 8 | calib[10]);
    dev->dig_P4 = (int16_t)(calib[13] << 8 | calib[12]);
    dev->dig_P5 = (int16_t)(calib[15] << 8 | calib[14]);
    dev->dig_P6 = (int16_t)(calib[17] << 8 | calib[16]);
    dev->dig_P7 = (int16_t)(calib[19] << 8 | calib[18]);
    dev->dig_P8 = (int16_t)(calib[21] << 8 | calib[20]);
    dev->dig_P9 = (int16_t)(calib[23] << 8 | calib[22]);
    dev->dig_H1 = calib[25];

    // Humidity calibration (registers 0xE1-0xE7)
    uint8_t hcalib[7];
    reg = BME280_REG_CALIB26;
    volt_i2c_write_read(bus_id, addr, &reg, 1, hcalib, 7);

    dev->dig_H2 = (int16_t)(hcalib[1] << 8 | hcalib[0]);
    dev->dig_H3 = hcalib[2];
    dev->dig_H4 = (int16_t)((hcalib[3] << 4) | (hcalib[4] & 0x0F));
    dev->dig_H5 = (int16_t)((hcalib[5] << 4) | (hcalib[4] >> 4));
    dev->dig_H6 = (int8_t)hcalib[6];

    // Configure: humidity oversampling x1
    volt_i2c_write_reg(bus_id, addr, BME280_REG_CTRL_HUM, 0x01);
    // Configure: temp oversampling x2, pressure oversampling x16, normal mode
    volt_i2c_write_reg(bus_id, addr, BME280_REG_CTRL_MEAS, 0x57);
    // Configure: standby 1s, filter coeff 4
    volt_i2c_write_reg(bus_id, addr, BME280_REG_CONFIG, 0xA0);

    log_info("bme280", "initialized at bus %d addr 0x%02X", bus_id, addr);
    return VOLT_OK;
}

float bme280_temperature(bme280_t *dev)
{
    // Read raw temperature (20-bit, registers 0xFA-0xFC)
    uint8_t data[3];
    uint8_t reg = 0xFA;
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, data, 3);
    int32_t raw = ((int32_t)data[0] << 12) | ((int32_t)data[1] << 4) | (data[2] >> 4);

    // Compensation formula from BME280 datasheet
    int32_t var1 = ((((raw >> 3) - ((int32_t)dev->dig_T1 << 1))) * ((int32_t)dev->dig_T2)) >> 11;
    int32_t var2 = (((((raw >> 4) - ((int32_t)dev->dig_T1)) *
                     ((raw >> 4) - ((int32_t)dev->dig_T1))) >> 12) *
                    ((int32_t)dev->dig_T3)) >> 14;
    dev->t_fine = var1 + var2;
    float temp = (float)((dev->t_fine * 5 + 128) >> 8) / 100.0f;
    return temp;
}

float bme280_humidity(bme280_t *dev)
{
    // Must read temperature first to get t_fine
    bme280_temperature(dev);

    uint8_t data[2];
    uint8_t reg = 0xFD;
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, data, 2);
    int32_t raw = ((int32_t)data[0] << 8) | data[1];

    int32_t v = dev->t_fine - 76800;
    v = (((((raw << 14) - (((int32_t)dev->dig_H4) << 20) -
           (((int32_t)dev->dig_H5) * v)) + 16384) >> 15) *
         (((((((v * ((int32_t)dev->dig_H6)) >> 10) *
              (((v * ((int32_t)dev->dig_H3)) >> 11) + 32768)) >> 10) + 2097152) *
           ((int32_t)dev->dig_H2) + 8192) >> 14));
    v = v - (((((v >> 15) * (v >> 15)) >> 7) * ((int32_t)dev->dig_H1)) >> 4);
    v = v < 0 ? 0 : v;
    v = v > 419430400 ? 419430400 : v;
    return (float)(v >> 12) / 1024.0f;
}

float bme280_pressure(bme280_t *dev)
{
    // Must read temperature first to get t_fine
    bme280_temperature(dev);

    uint8_t data[3];
    uint8_t reg = 0xF7;
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, data, 3);
    int32_t raw = ((int32_t)data[0] << 12) | ((int32_t)data[1] << 4) | (data[2] >> 4);

    int64_t var1 = ((int64_t)dev->t_fine) - 128000;
    int64_t var2 = var1 * var1 * (int64_t)dev->dig_P6;
    var2 = var2 + ((var1 * (int64_t)dev->dig_P5) << 17);
    var2 = var2 + (((int64_t)dev->dig_P4) << 35);
    var1 = ((var1 * var1 * (int64_t)dev->dig_P3) >> 8) +
           ((var1 * (int64_t)dev->dig_P2) << 12);
    var1 = (((((int64_t)1) << 47) + var1)) * ((int64_t)dev->dig_P1) >> 33;
    if (var1 == 0) return 0.0f;

    int64_t p = 1048576 - raw;
    p = (((p << 31) - var2) * 3125) / var1;
    var1 = (((int64_t)dev->dig_P9) * (p >> 13) * (p >> 13)) >> 25;
    var2 = (((int64_t)dev->dig_P8) * p) >> 19;
    p = ((p + var1 + var2) >> 8) + (((int64_t)dev->dig_P7) << 4);
    return (float)p / 25600.0f; // hPa
}
