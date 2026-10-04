#include "volt/i2c.h"
#include "volt/log.h"
#include "pico/stdlib.h"
#include "hardware/i2c.h"

// RP2040 has 2 I2C peripherals: i2c0, i2c1
static i2c_inst_t *get_i2c_hw(uint8_t bus_id)
{
    return bus_id == 0 ? i2c0 : i2c1;
}

// Rename Volt's i2c_init to avoid conflict with pico-sdk's i2c_init
volt_error_t volt_i2c_init(uint8_t bus_id, uint8_t sda, uint8_t scl, uint32_t speed)
{
    i2c_inst_t *hw = get_i2c_hw(bus_id);

    uint baud = i2c_init(hw, speed ? speed : 400000);

    gpio_set_function(sda, GPIO_FUNC_I2C);
    gpio_set_function(scl, GPIO_FUNC_I2C);
    gpio_pull_up(sda);
    gpio_pull_up(scl);

    log_info("i2c", "bus %d initialized (SDA=%d SCL=%d speed=%lu actual=%lu)",
             bus_id, sda, scl, (unsigned long)(speed ? speed : 400000), (unsigned long)baud);
    return VOLT_OK;
}

volt_error_t volt_i2c_write(uint8_t bus_id, uint8_t addr, const uint8_t *data, uint8_t len)
{
    i2c_inst_t *i2c = get_i2c_hw(bus_id);
    int ret = i2c_write_blocking(i2c, addr, data, len, false);
    return ret == len ? VOLT_OK : VOLT_ERR_INTERNAL;
}

volt_error_t volt_i2c_read(uint8_t bus_id, uint8_t addr, uint8_t *buf, uint8_t len)
{
    i2c_inst_t *i2c = get_i2c_hw(bus_id);
    int ret = i2c_read_blocking(i2c, addr, buf, len, false);
    return ret == len ? VOLT_OK : VOLT_ERR_INTERNAL;
}

volt_error_t volt_i2c_write_read(uint8_t bus_id, uint8_t addr,
                            const uint8_t *write_data, uint8_t write_len,
                            uint8_t *read_buf, uint8_t read_len)
{
    i2c_inst_t *i2c = get_i2c_hw(bus_id);
    // Write with nostop=true (repeated start)
    int ret = i2c_write_blocking(i2c, addr, write_data, write_len, true);
    if (ret != write_len) return VOLT_ERR_INTERNAL;
    ret = i2c_read_blocking(i2c, addr, read_buf, read_len, false);
    return ret == read_len ? VOLT_OK : VOLT_ERR_INTERNAL;
}

uint8_t volt_i2c_read_reg(uint8_t bus_id, uint8_t addr, uint8_t reg)
{
    uint8_t val = 0;
    volt_i2c_write_read(bus_id, addr, &reg, 1, &val, 1);
    return val;
}

volt_error_t volt_i2c_write_reg(uint8_t bus_id, uint8_t addr, uint8_t reg, uint8_t value)
{
    uint8_t data[2] = { reg, value };
    return volt_i2c_write(bus_id, addr, data, 2);
}

uint16_t volt_i2c_read_reg16(uint8_t bus_id, uint8_t addr, uint8_t reg)
{
    uint8_t buf[2] = {0};
    volt_i2c_write_read(bus_id, addr, &reg, 1, buf, 2);
    return (buf[0] << 8) | buf[1];
}
