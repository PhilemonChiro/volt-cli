// hc05 driver — framework includes added on install
#include <stdint.h>
#include <stdbool.h>
#include <stddef.h>
#include <string.h>
#include <stdio.h>
#include "volt/sensor.h"
#include "volt/uart.h"
#include "volt/task.h"
#include "volt/log.h"

#include <stdint.h>
#include <stdbool.h>
#include <string.h>

volt_error_t hc05_init(hc05_t *dev, uint8_t uart_id) {
    dev->uart_id = uart_id;
    return VOLT_OK;
}

volt_error_t hc05_send_data(hc05_t *dev, int data) {
    char buffer[12];
    snprintf(buffer, sizeof(buffer), "%d", data);
    return volt_uart_print(dev->uart_id, buffer);
}

int hc05_receive_data(hc05_t *dev, char *buffer, uint16_t max_len) {
    if (volt_uart_available(dev->uart_id) > 0) {
        return volt_uart_read(dev->uart_id, (uint8_t *)buffer, max_len);
    }
    return 0;
}

volt_error_t hc05_send_at_command(hc05_t *dev, const char *command) {
    volt_error_t err = volt_uart_print(dev->uart_id, command);
    if (err != VOLT_OK) return err;
    return volt_uart_print(dev->uart_id, "\r\n");
}

int32_t hc05_read_at_response(hc05_t *dev, char *response, uint16_t max_len) {
    int len = 0;
    while (volt_uart_available(dev->uart_id) > 0 && len < max_len - 1) {
        len += volt_uart_read(dev->uart_id, (uint8_t *)response + len, 1);
    }
    response[len] = '\0';
    return len;
}

volt_error_t hc05_set_baud_rate(hc05_t *dev, int baud_rate) {
    char command[20];
    snprintf(command, sizeof(command), "AT+UART=%d,0,0", baud_rate);
    return hc05_send_at_command(dev, command);
}

volt_error_t hc05_set_name(hc05_t *dev, const char *name) {
    char command[30];
    snprintf(command, sizeof(command), "AT+NAME=%s", name);
    return hc05_send_at_command(dev, command);
}

volt_error_t hc05_set_password(hc05_t *dev, const char *password) {
    char command[30];
    snprintf(command, sizeof(command), "AT+PSWD=%s", password);
    return hc05_send_at_command(dev, command);
}
