/* SHT31 High-accuracy Temperature + Humidity driver (I2C address: 0x44) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include "volt/task.h"

volt_error_t sht31_init(sht31_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x44;

    // Soft reset
    uint8_t cmd[2] = { 0x30, 0xA2 };
    volt_i2c_write(bus_id, dev->addr, cmd, 2);

    log_info("sht31", "initialized at bus %d addr 0x%02X", bus_id, dev->addr);
    return VOLT_OK;
}

static void sht31_read_raw(sht31_t *dev)
{
    // Trigger single-shot measurement, high repeatability
    uint8_t cmd[2] = { 0x24, 0x00 };
    volt_i2c_write(dev->bus_id, dev->addr, cmd, 2);

    // Wait 15ms for measurement
    volt_delay_ms(15);

    // Read 6 bytes: temp_msb, temp_lsb, temp_crc, hum_msb, hum_lsb, hum_crc
    uint8_t data[6] = {0};
    volt_i2c_read(dev->bus_id, dev->addr, data, 6);

    uint16_t raw_temp = (data[0] << 8) | data[1];
    uint16_t raw_hum  = (data[3] << 8) | data[4];

    dev->last_temperature = -45.0f + 175.0f * ((float)raw_temp / 65535.0f);
    dev->last_humidity    = 100.0f * ((float)raw_hum / 65535.0f);
}

float sht31_temperature(sht31_t *dev)
{
    sht31_read_raw(dev);
    return dev->last_temperature;
}

float sht31_humidity(sht31_t *dev)
{
    sht31_read_raw(dev);
    return dev->last_humidity;
}
