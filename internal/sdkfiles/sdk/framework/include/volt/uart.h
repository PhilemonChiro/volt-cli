#ifndef VOLT_UART_H
#define VOLT_UART_H

#include <stdint.h>
#include "volt/error.h"

// Initialize a UART port with TX, RX pins and baud rate.
// port_id: 0-2 on ESP32, 0-1 on RP2040
volt_error_t volt_uart_init(uint8_t port_id, uint8_t tx_pin, uint8_t rx_pin, uint32_t baud);

// Write bytes to UART.
volt_error_t volt_uart_write(uint8_t port_id, const uint8_t *data, uint16_t len);

// Write a null-terminated string.
volt_error_t volt_uart_print(uint8_t port_id, const char *text);

// Read available bytes into buffer. Returns number of bytes read.
int volt_uart_read(uint8_t port_id, uint8_t *buf, uint16_t max_len);

// Read a single byte. Returns -1 if no data available.
int volt_uart_read_byte(uint8_t port_id);

// Read until newline or max_len. Returns length read.
int volt_uart_read_line(uint8_t port_id, char *buf, uint16_t max_len);

// Number of bytes available to read.
int volt_uart_available(uint8_t port_id);

// Flush TX buffer (wait until all data sent).
void volt_uart_flush(uint8_t port_id);

#endif // VOLT_UART_H
