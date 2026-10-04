/* MAX30102 Pulse Oximeter + Heart Rate Sensor (I2C addr: 0x57)
   Datasheet: Maxim Integrated MAX30102 */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"
#include "volt/task.h"

#define MAX30102_REG_INT_STATUS1  0x00
#define MAX30102_REG_INT_STATUS2  0x01
#define MAX30102_REG_INT_ENABLE1  0x02
#define MAX30102_REG_INT_ENABLE2  0x03
#define MAX30102_REG_FIFO_WR_PTR  0x04
#define MAX30102_REG_FIFO_OVF     0x05
#define MAX30102_REG_FIFO_RD_PTR  0x06
#define MAX30102_REG_FIFO_DATA    0x07
#define MAX30102_REG_FIFO_CONFIG  0x08
#define MAX30102_REG_MODE_CONFIG  0x09
#define MAX30102_REG_SPO2_CONFIG  0x0A
#define MAX30102_REG_LED1_PA      0x0C  // Red LED current
#define MAX30102_REG_LED2_PA      0x0D  // IR LED current
#define MAX30102_REG_PILOT_PA     0x10
#define MAX30102_REG_MULTI_LED1   0x11
#define MAX30102_REG_TEMP_INT     0x1F
#define MAX30102_REG_TEMP_FRAC    0x20
#define MAX30102_REG_TEMP_CONFIG  0x21
#define MAX30102_REG_PART_ID      0xFE  // Should be 0x15
#define MAX30102_REG_REV_ID       0xFF

volt_error_t max30102_init(max30102_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x57;

    // Verify part ID
    uint8_t part_id = volt_i2c_read_reg(bus_id, dev->addr, MAX30102_REG_PART_ID);
    if (part_id != 0x15) {
        log_error("max30102", "wrong part ID: 0x%02X (expected 0x15)", part_id);
        return VOLT_ERR_INTERNAL;
    }

    // Reset
    volt_i2c_write_reg(bus_id, dev->addr, MAX30102_REG_MODE_CONFIG, 0x40);
    volt_delay_ms(100);

    // Clear FIFO pointers
    volt_i2c_write_reg(bus_id, dev->addr, MAX30102_REG_FIFO_WR_PTR, 0x00);
    volt_i2c_write_reg(bus_id, dev->addr, MAX30102_REG_FIFO_OVF, 0x00);
    volt_i2c_write_reg(bus_id, dev->addr, MAX30102_REG_FIFO_RD_PTR, 0x00);

    // FIFO config: sample averaging = 4, FIFO rollover enabled
    volt_i2c_write_reg(bus_id, dev->addr, MAX30102_REG_FIFO_CONFIG, 0x4F);

    // SpO2 mode (Red + IR LEDs)
    volt_i2c_write_reg(bus_id, dev->addr, MAX30102_REG_MODE_CONFIG, 0x03);

    // SpO2 config: ADC range 4096nA, 100 samples/sec, 18-bit resolution
    volt_i2c_write_reg(bus_id, dev->addr, MAX30102_REG_SPO2_CONFIG, 0x27);

    // LED currents: ~7mA for both (0x24 = 7.2mA)
    volt_i2c_write_reg(bus_id, dev->addr, MAX30102_REG_LED1_PA, 0x24);
    volt_i2c_write_reg(bus_id, dev->addr, MAX30102_REG_LED2_PA, 0x24);

    log_info("max30102", "pulse oximeter initialized at 0x%02X", dev->addr);
    return VOLT_OK;
}

static void max30102_read_fifo(max30102_t *dev)
{
    // Read one sample from FIFO (6 bytes: 3 for Red, 3 for IR)
    uint8_t data[6];
    uint8_t reg = MAX30102_REG_FIFO_DATA;
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, data, 6);

    // 18-bit values (top 2 bits of each 3-byte group are unused)
    dev->red_value = ((uint32_t)(data[0] & 0x03) << 16) |
                     ((uint32_t)data[1] << 8) | data[2];
    dev->ir_value  = ((uint32_t)(data[3] & 0x03) << 16) |
                     ((uint32_t)data[4] << 8) | data[5];
}

int max30102_red(max30102_t *dev)
{
    max30102_read_fifo(dev);
    return (int)dev->red_value;
}

int max30102_ir(max30102_t *dev)
{
    max30102_read_fifo(dev);
    return (int)dev->ir_value;
}

float max30102_temperature(max30102_t *dev)
{
    // Trigger temperature measurement
    volt_i2c_write_reg(dev->bus_id, dev->addr, MAX30102_REG_TEMP_CONFIG, 0x01);
    volt_delay_ms(50);

    int8_t temp_int = (int8_t)volt_i2c_read_reg(dev->bus_id, dev->addr, MAX30102_REG_TEMP_INT);
    uint8_t temp_frac = volt_i2c_read_reg(dev->bus_id, dev->addr, MAX30102_REG_TEMP_FRAC);

    dev->temperature = (float)temp_int + (float)temp_frac * 0.0625f;
    return dev->temperature;
}

int max30102_is_finger_present(max30102_t *dev)
{
    max30102_read_fifo(dev);
    // IR value > 50000 typically indicates finger is on sensor
    return dev->ir_value > 50000 ? 1 : 0;
}
