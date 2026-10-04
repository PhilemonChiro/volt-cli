#ifndef VOLT_SPI_H
#define VOLT_SPI_H

#include <stdint.h>
#include "volt/error.h"

// Initialize an SPI bus with MOSI, MISO, CLK pins.
// speed: clock frequency in Hz (e.g. 1000000 = 1MHz, 10000000 = 10MHz)
volt_error_t volt_spi_init(uint8_t bus_id, uint8_t mosi, uint8_t miso,
                           uint8_t clk, uint32_t speed);

// Assert (pull LOW) the chip-select pin for a device.
void volt_spi_cs_select(uint8_t cs_pin);

// Deassert (pull HIGH) the chip-select pin.
void volt_spi_cs_deselect(uint8_t cs_pin);

// Write bytes to SPI (MOSI only, discard MISO).
volt_error_t volt_spi_write(uint8_t bus_id, const uint8_t *data, uint8_t len);

// Read bytes from SPI (send dummy 0x00 on MOSI, capture MISO).
volt_error_t volt_spi_read(uint8_t bus_id, uint8_t *buf, uint8_t len);

// Full-duplex transfer: write MOSI and read MISO simultaneously.
volt_error_t volt_spi_transfer(uint8_t bus_id, const uint8_t *tx, uint8_t *rx, uint8_t len);

// Write a command byte, then read response bytes.
// Common pattern: send register address, read data back.
volt_error_t volt_spi_write_then_read(uint8_t bus_id, uint8_t cs_pin,
                                      const uint8_t *write_data, uint8_t write_len,
                                      uint8_t *read_buf, uint8_t read_len);

#endif // VOLT_SPI_H
