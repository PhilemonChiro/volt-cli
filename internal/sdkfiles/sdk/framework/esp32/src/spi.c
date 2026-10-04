/* ESP32 SPI Master driver — uses ESP-IDF spi_master API */

#include "volt/spi.h"
#include "volt/log.h"
#include "driver/spi_master.h"
#include "driver/gpio.h"
#include <string.h>

// ESP32 has HSPI (SPI2) and VSPI (SPI3) available for application use
static spi_device_handle_t spi_handles[2] = { NULL, NULL };
static bool spi_bus_initialized[2] = { false, false };

volt_error_t volt_spi_init(uint8_t bus_id, uint8_t mosi, uint8_t miso,
                           uint8_t clk, uint32_t speed)
{
    if (bus_id > 1) return VOLT_ERR_INVALID_ARG;

    spi_host_device_t host = (bus_id == 0) ? SPI2_HOST : SPI3_HOST;

    spi_bus_config_t bus_cfg = {
        .mosi_io_num = mosi,
        .miso_io_num = miso,
        .sclk_io_num = clk,
        .quadwp_io_num = -1,
        .quadhd_io_num = -1,
        .max_transfer_sz = 4096,
    };

    esp_err_t err = spi_bus_initialize(host, &bus_cfg, SPI_DMA_CH_AUTO);
    if (err != ESP_OK && err != ESP_ERR_INVALID_STATE) {
        log_error("spi", "bus %d init failed (err=%d)", bus_id, err);
        return VOLT_ERR_INTERNAL;
    }

    // Add a default device (no CS — CS managed manually per-device)
    spi_device_interface_config_t dev_cfg = {
        .mode = 0,
        .clock_speed_hz = speed ? speed : 1000000,
        .spics_io_num = -1,  // CS managed manually
        .queue_size = 1,
    };

    err = spi_bus_add_device(host, &dev_cfg, &spi_handles[bus_id]);
    if (err != ESP_OK) {
        log_error("spi", "add device failed on bus %d (err=%d)", bus_id, err);
        return VOLT_ERR_INTERNAL;
    }

    spi_bus_initialized[bus_id] = true;
    log_info("spi", "bus %d initialized (MOSI=%d MISO=%d CLK=%d speed=%lu)",
             bus_id, mosi, miso, clk, (unsigned long)speed);
    return VOLT_OK;
}

void volt_spi_cs_select(uint8_t cs_pin)
{
    gpio_set_direction(cs_pin, GPIO_MODE_OUTPUT);
    gpio_set_level(cs_pin, 0);
}

void volt_spi_cs_deselect(uint8_t cs_pin)
{
    gpio_set_level(cs_pin, 1);
}

volt_error_t volt_spi_write(uint8_t bus_id, const uint8_t *data, uint8_t len)
{
    if (bus_id > 1 || !spi_handles[bus_id]) return VOLT_ERR_INTERNAL;

    spi_transaction_t trans = {
        .length = len * 8,
        .tx_buffer = data,
        .rx_buffer = NULL,
    };

    esp_err_t err = spi_device_transmit(spi_handles[bus_id], &trans);
    return err == ESP_OK ? VOLT_OK : VOLT_ERR_INTERNAL;
}

volt_error_t volt_spi_read(uint8_t bus_id, uint8_t *buf, uint8_t len)
{
    if (bus_id > 1 || !spi_handles[bus_id]) return VOLT_ERR_INTERNAL;

    spi_transaction_t trans = {
        .length = len * 8,
        .rxlength = len * 8,
        .tx_buffer = NULL,
        .rx_buffer = buf,
    };

    esp_err_t err = spi_device_transmit(spi_handles[bus_id], &trans);
    return err == ESP_OK ? VOLT_OK : VOLT_ERR_INTERNAL;
}

volt_error_t volt_spi_transfer(uint8_t bus_id, const uint8_t *tx, uint8_t *rx, uint8_t len)
{
    if (bus_id > 1 || !spi_handles[bus_id]) return VOLT_ERR_INTERNAL;

    spi_transaction_t trans = {
        .length = len * 8,
        .tx_buffer = tx,
        .rx_buffer = rx,
    };

    esp_err_t err = spi_device_transmit(spi_handles[bus_id], &trans);
    return err == ESP_OK ? VOLT_OK : VOLT_ERR_INTERNAL;
}

volt_error_t volt_spi_write_then_read(uint8_t bus_id, uint8_t cs_pin,
                                      const uint8_t *write_data, uint8_t write_len,
                                      uint8_t *read_buf, uint8_t read_len)
{
    volt_spi_cs_select(cs_pin);
    volt_error_t err = volt_spi_write(bus_id, write_data, write_len);
    if (err == VOLT_OK) {
        err = volt_spi_read(bus_id, read_buf, read_len);
    }
    volt_spi_cs_deselect(cs_pin);
    return err;
}
