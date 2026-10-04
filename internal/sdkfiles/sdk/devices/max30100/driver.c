/* MAX30100 Pulse Oximeter + Heart Rate Sensor (I2C addr: 0x57)
   Older version of MAX30102 — 16-bit data, different register map */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include "volt/task.h"

#define MAX30100_REG_INT_STATUS   0x00
#define MAX30100_REG_INT_ENABLE   0x01
#define MAX30100_REG_FIFO_WR      0x02
#define MAX30100_REG_FIFO_OVF     0x03
#define MAX30100_REG_FIFO_RD      0x04
#define MAX30100_REG_FIFO_DATA    0x05
#define MAX30100_REG_MODE_CONFIG  0x06
#define MAX30100_REG_SPO2_CONFIG  0x07
#define MAX30100_REG_LED_CONFIG   0x09
#define MAX30100_REG_TEMP_INT     0x16
#define MAX30100_REG_TEMP_FRAC    0x17
#define MAX30100_REG_PART_ID      0xFF  // Should be 0x11

volt_error_t max30100_init(max30100_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x57;

    uint8_t part_id = volt_i2c_read_reg(bus_id, dev->addr, MAX30100_REG_PART_ID);
    if (part_id != 0x11) {
        log_error("max30100", "wrong part ID: 0x%02X (expected 0x11)", part_id);
        return VOLT_ERR_INTERNAL;
    }

    // SpO2 mode
    volt_i2c_write_reg(bus_id, dev->addr, MAX30100_REG_MODE_CONFIG, 0x03);
    // SpO2 config: 100 samples/sec, 16-bit resolution
    volt_i2c_write_reg(bus_id, dev->addr, MAX30100_REG_SPO2_CONFIG, 0x47);
    // LED currents: Red 11mA, IR 11mA
    volt_i2c_write_reg(bus_id, dev->addr, MAX30100_REG_LED_CONFIG, 0x33);

    log_info("max30100", "pulse oximeter initialized at 0x%02X", dev->addr);
    return VOLT_OK;
}

static void max30100_read_fifo(max30100_t *dev)
{
    uint8_t data[4]; // 2 bytes IR + 2 bytes Red (16-bit each)
    uint8_t reg = MAX30100_REG_FIFO_DATA;
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, data, 4);

    dev->ir_value  = ((uint16_t)data[0] << 8) | data[1];
    dev->red_value = ((uint16_t)data[2] << 8) | data[3];
}

int max30100_red(max30100_t *dev)  { max30100_read_fifo(dev); return dev->red_value; }
int max30100_ir(max30100_t *dev)   { max30100_read_fifo(dev); return dev->ir_value; }

float max30100_temperature(max30100_t *dev)
{
    // Trigger temp measurement (set bit 3 in mode config)
    uint8_t mode = volt_i2c_read_reg(dev->bus_id, dev->addr, MAX30100_REG_MODE_CONFIG);
    volt_i2c_write_reg(dev->bus_id, dev->addr, MAX30100_REG_MODE_CONFIG, mode | 0x08);
    volt_delay_ms(50);

    int8_t temp_int = (int8_t)volt_i2c_read_reg(dev->bus_id, dev->addr, MAX30100_REG_TEMP_INT);
    uint8_t temp_frac = volt_i2c_read_reg(dev->bus_id, dev->addr, MAX30100_REG_TEMP_FRAC);
    dev->temperature = (float)temp_int + (float)temp_frac * 0.0625f;
    return dev->temperature;
}

int max30100_is_finger_present(max30100_t *dev)
{
    max30100_read_fifo(dev);
    return dev->ir_value > 10000 ? 1 : 0;
}
