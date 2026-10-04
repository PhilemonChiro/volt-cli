#ifndef VOLT_I2C_H
#define VOLT_I2C_H

#include <stdint.h>
#include "volt/error.h"

// Initialize an I2C bus with SDA and SCL pins.
// speed: clock frequency in Hz (100000 = standard, 400000 = fast)
volt_error_t volt_i2c_init(uint8_t bus_id, uint8_t sda, uint8_t scl, uint32_t speed);

// Write bytes to a device on the bus.
volt_error_t volt_i2c_write(uint8_t bus_id, uint8_t addr, const uint8_t *data, uint8_t len);

// Read bytes from a device on the bus.
volt_error_t volt_i2c_read(uint8_t bus_id, uint8_t addr, uint8_t *buf, uint8_t len);

// Write a register address, then read data (most common sensor pattern).
volt_error_t volt_i2c_write_read(uint8_t bus_id, uint8_t addr,
                            const uint8_t *write_data, uint8_t write_len,
                            uint8_t *read_buf, uint8_t read_len);

// Read a single byte from a register.
uint8_t volt_i2c_read_reg(uint8_t bus_id, uint8_t addr, uint8_t reg);

// Write a single byte to a register.
volt_error_t volt_i2c_write_reg(uint8_t bus_id, uint8_t addr, uint8_t reg, uint8_t value);

// Read a 16-bit big-endian value from a register.
uint16_t volt_i2c_read_reg16(uint8_t bus_id, uint8_t addr, uint8_t reg);

#endif // VOLT_I2C_H
