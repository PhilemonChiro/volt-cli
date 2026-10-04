/* AHT20 Temperature + Humidity sensor driver (I2C address: 0x38) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include "volt/task.h"  // for volt_delay_ms()

#define AHT20_ADDR        0x38
#define AHT20_CMD_INIT    0xBE
#define AHT20_CMD_TRIGGER 0xAC
#define AHT20_CMD_STATUS  0x71

volt_error_t aht20_init(aht20_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : AHT20_ADDR;

    // Check calibration status
    uint8_t cmd = AHT20_CMD_STATUS;
    uint8_t status = 0;
    volt_i2c_write(bus_id, dev->addr, &cmd, 1);
    volt_i2c_read(bus_id, dev->addr, &status, 1);

    if (!(status & 0x08)) {
        // Not calibrated — send init command
        uint8_t init_cmd[3] = { AHT20_CMD_INIT, 0x08, 0x00 };
        volt_i2c_write(bus_id, dev->addr, init_cmd, 3);
    }

    log_info("aht20", "initialized at bus %d addr 0x%02X", bus_id, dev->addr);
    return VOLT_OK;
}

static void aht20_read_raw(aht20_t *dev)
{
    // Trigger measurement
    uint8_t cmd[3] = { AHT20_CMD_TRIGGER, 0x33, 0x00 };
    volt_i2c_write(dev->bus_id, dev->addr, cmd, 3);

    // Wait for measurement (80ms)
    volt_delay_ms(80);

    // Read 6 bytes
    uint8_t data[6] = {0};
    volt_i2c_read(dev->bus_id, dev->addr, data, 6);

    // Parse raw values (20-bit each)
    uint32_t raw_hum  = ((uint32_t)(data[1]) << 12) | ((uint32_t)(data[2]) << 4) | (data[3] >> 4);
    uint32_t raw_temp = (((uint32_t)(data[3] & 0x0F)) << 16) | ((uint32_t)(data[4]) << 8) | data[5];

    dev->last_humidity    = (raw_hum * 100.0f) / 1048576.0f;
    dev->last_temperature = (raw_temp * 200.0f) / 1048576.0f - 50.0f;
}

float aht20_temperature(aht20_t *dev)
{
    aht20_read_raw(dev);
    return dev->last_temperature;
}

float aht20_humidity(aht20_t *dev)
{
    aht20_read_raw(dev);
    return dev->last_humidity;
}
