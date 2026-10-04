/* BH1750 Ambient Light Sensor driver
   Works on ESP32 and RP2040 — uses the portable volt_i2c_ functions */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"

#define BH1750_CMD_POWER_ON    0x01
#define BH1750_CMD_CONT_HRES   0x10  // Continuous high-resolution mode (1 lux)

volt_error_t bh1750_init(bh1750_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr;

    // Power on
    uint8_t cmd = BH1750_CMD_POWER_ON;
    volt_error_t err = volt_i2c_write(bus_id, addr, &cmd, 1);
    if (err != VOLT_OK) {
        log_error("bh1750", "power on failed at bus %d addr 0x%02X", bus_id, addr);
        return err;
    }

    // Start continuous measurement
    cmd = BH1750_CMD_CONT_HRES;
    volt_i2c_write(bus_id, addr, &cmd, 1);

    log_info("bh1750", "initialized at bus %d addr 0x%02X", bus_id, addr);
    return VOLT_OK;
}

float bh1750_lux(bh1750_t *dev)
{
    uint8_t data[2] = {0};
    volt_i2c_read(dev->bus_id, dev->addr, data, 2);
    uint16_t raw = (data[0] << 8) | data[1];
    return (float)raw / 1.2f;
}
