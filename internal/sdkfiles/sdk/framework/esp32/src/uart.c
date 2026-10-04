/* ESP32 UART driver — uses ESP-IDF driver/uart.h
   Supports UART0 (USB serial), UART1, UART2 */

#include "volt/uart.h"
#include "volt/log.h"
#include "driver/uart.h"
#include "driver/gpio.h"

#define UART_BUF_SIZE 1024

static bool uart_initialized[3] = { false, false, false };

volt_error_t volt_uart_init(uint8_t port_id, uint8_t tx_pin, uint8_t rx_pin, uint32_t baud)
{
    if (port_id > 2) return VOLT_ERR_INVALID_ARG;

    uart_config_t cfg = {
        .baud_rate = baud ? baud : 9600,
        .data_bits = UART_DATA_8_BITS,
        .parity    = UART_PARITY_DISABLE,
        .stop_bits = UART_STOP_BITS_1,
        .flow_ctrl = UART_HW_FLOWCTRL_DISABLE,
        .source_clk = UART_SCLK_DEFAULT,
    };

    esp_err_t err = uart_param_config(port_id, &cfg);
    if (err != ESP_OK) {
        log_error("uart", "config failed on port %d (err=%d)", port_id, err);
        return VOLT_ERR_INTERNAL;
    }

    // Set pins (use -1 for RTS/CTS since we don't use flow control)
    uart_set_pin(port_id, tx_pin, rx_pin, UART_PIN_NO_CHANGE, UART_PIN_NO_CHANGE);

    // Install driver with RX buffer
    if (!uart_initialized[port_id]) {
        err = uart_driver_install(port_id, UART_BUF_SIZE, 0, 0, NULL, 0);
        if (err != ESP_OK && err != ESP_ERR_INVALID_STATE) {
            log_error("uart", "driver install failed on port %d", port_id);
            return VOLT_ERR_INTERNAL;
        }
        uart_initialized[port_id] = true;
    }

    log_info("uart", "port %d initialized (TX=%d RX=%d baud=%lu)",
             port_id, tx_pin, rx_pin, (unsigned long)baud);
    return VOLT_OK;
}

volt_error_t volt_uart_write(uint8_t port_id, const uint8_t *data, uint16_t len)
{
    if (port_id > 2) return VOLT_ERR_INVALID_ARG;
    int written = uart_write_bytes(port_id, data, len);
    return (written == len) ? VOLT_OK : VOLT_ERR_INTERNAL;
}

volt_error_t volt_uart_print(uint8_t port_id, const char *text)
{
    if (port_id > 2) return VOLT_ERR_INVALID_ARG;
    if (text == NULL) return VOLT_ERR_INVALID_ARG;
    int len = 0;
    while (text[len]) len++;
    int written = uart_write_bytes(port_id, text, len);
    return (written == len) ? VOLT_OK : VOLT_ERR_INTERNAL;
}

int volt_uart_read(uint8_t port_id, uint8_t *buf, uint16_t max_len)
{
    if (port_id > 2) return 0;
    int len = uart_read_bytes(port_id, buf, max_len, 0);
    return (len > 0) ? len : 0;
}

int volt_uart_read_byte(uint8_t port_id)
{
    uint8_t byte;
    int len = uart_read_bytes(port_id, &byte, 1, 0);
    return (len == 1) ? byte : -1;
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
    if (port_id > 2) return 0;
    size_t len = 0;
    uart_get_buffered_data_len(port_id, &len);
    return (int)len;
}

void volt_uart_flush(uint8_t port_id)
{
    if (port_id > 2) return;
    uart_wait_tx_done(port_id, 1000 / portTICK_PERIOD_MS);
}
