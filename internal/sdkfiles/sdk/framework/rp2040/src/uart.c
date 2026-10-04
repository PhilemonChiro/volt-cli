/* RP2040 UART driver — uses pico-sdk hardware/uart.h
   Supports UART0 and UART1 */

#include "volt/uart.h"
#include "volt/log.h"
#include "pico/stdlib.h"
#include "hardware/uart.h"
#include "hardware/gpio.h"

static uart_inst_t *get_uart_hw(uint8_t port_id)
{
    return port_id == 0 ? uart0 : uart1;
}

volt_error_t volt_uart_init(uint8_t port_id, uint8_t tx_pin, uint8_t rx_pin, uint32_t baud)
{
    if (port_id > 1) return VOLT_ERR_INVALID_ARG;

    uart_inst_t *hw = get_uart_hw(port_id);
    uint actual = uart_init(hw, baud ? baud : 9600);

    gpio_set_function(tx_pin, GPIO_FUNC_UART);
    gpio_set_function(rx_pin, GPIO_FUNC_UART);

    // Enable FIFO
    uart_set_fifo_enabled(hw, true);

    log_info("uart", "port %d initialized (TX=%d RX=%d baud=%lu actual=%lu)",
             port_id, tx_pin, rx_pin, (unsigned long)(baud ? baud : 9600),
             (unsigned long)actual);
    return VOLT_OK;
}

volt_error_t volt_uart_write(uint8_t port_id, const uint8_t *data, uint16_t len)
{
    uart_inst_t *hw = get_uart_hw(port_id);
    uart_write_blocking(hw, data, len);
    return VOLT_OK;
}

volt_error_t volt_uart_print(uint8_t port_id, const char *text)
{
    if (text == NULL) return VOLT_ERR_INVALID_ARG;
    uart_inst_t *hw = get_uart_hw(port_id);
    while (*text) {
        uart_putc(hw, *text++);
    }
    return VOLT_OK;
}

int volt_uart_read(uint8_t port_id, uint8_t *buf, uint16_t max_len)
{
    uart_inst_t *hw = get_uart_hw(port_id);
    int count = 0;
    while (count < max_len && uart_is_readable(hw)) {
        buf[count++] = uart_getc(hw);
    }
    return count;
}

int volt_uart_read_byte(uint8_t port_id)
{
    uart_inst_t *hw = get_uart_hw(port_id);
    if (uart_is_readable(hw)) {
        return uart_getc(hw);
    }
    return -1;
}

int volt_uart_read_line(uint8_t port_id, char *buf, uint16_t max_len)
{
    int pos = 0;
    while (pos < max_len - 1) {
        int c = volt_uart_read_byte(port_id);
        if (c < 0) break;
        if (c == '\n') break;
        if (c == '\r') continue;
        buf[pos++] = (char)c;
    }
    buf[pos] = '\0';
    return pos;
}

int volt_uart_available(uint8_t port_id)
{
    uart_inst_t *hw = get_uart_hw(port_id);
    if (!hw) return 0;
    // RP2040 UART FIFO doesn't expose a byte count; returns 0 or 1
    return uart_is_readable(hw) ? 1 : 0;
}

void volt_uart_flush(uint8_t port_id)
{
    uart_inst_t *hw = get_uart_hw(port_id);
    uart_tx_wait_blocking(hw);
}
