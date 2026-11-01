/* RP2040 SPI driver — uses pico-sdk hardware/spi.h */

#include "volt/spi.h"
#include "volt/log.h"
#include "pico/stdlib.h"
#include "hardware/spi.h"
#include "hardware/gpio.h"

static spi_inst_t *get_spi_hw(uint8_t bus_id)
{
    return bus_id == 0 ? spi0 : spi1;
}

volt_error_t volt_spi_init(uint8_t bus_id, uint8_t mosi, uint8_t miso,
                           uint8_t clk, uint32_t speed)
{
    spi_inst_t *spi = get_spi_hw(bus_id);

    uint actual = spi_init(spi, speed ? speed : 1000000);

    gpio_set_function(mosi, GPIO_FUNC_SPI);
    gpio_set_function(miso, GPIO_FUNC_SPI);
    gpio_set_function(clk, GPIO_FUNC_SPI);

    log_info("spi", "bus %d initialized (MOSI=%d MISO=%d CLK=%d speed=%lu actual=%lu)",
             bus_id, mosi, miso, clk, (unsigned long)(speed ? speed : 1000000),
             (unsigned long)actual);
    return VOLT_OK;
}

void volt_spi_cs_select(uint8_t cs_pin)
{
    gpio_init(cs_pin);
    gpio_set_dir(cs_pin, GPIO_OUT);
    gpio_put(cs_pin, 0);
}

void volt_spi_cs_deselect(uint8_t cs_pin)
{
    gpio_put(cs_pin, 1);
}

volt_error_t volt_spi_write(uint8_t bus_id, const uint8_t *data, uint8_t len)
{
    spi_inst_t *spi = get_spi_hw(bus_id);
    int ret = spi_write_blocking(spi, data, len);
    return volt_note_error(ret == len ? VOLT_OK : VOLT_ERR_INTERNAL);
}

volt_error_t volt_spi_read(uint8_t bus_id, uint8_t *buf, uint8_t len)
{
    spi_inst_t *spi = get_spi_hw(bus_id);
    int ret = spi_read_blocking(spi, 0x00, buf, len);
    return volt_note_error(ret == len ? VOLT_OK : VOLT_ERR_INTERNAL);
}

volt_error_t volt_spi_transfer(uint8_t bus_id, const uint8_t *tx, uint8_t *rx, uint8_t len)
{
    spi_inst_t *spi = get_spi_hw(bus_id);
    int ret = spi_write_read_blocking(spi, tx, rx, len);
    return volt_note_error(ret == len ? VOLT_OK : VOLT_ERR_INTERNAL);
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
