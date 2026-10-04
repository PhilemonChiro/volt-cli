#include "volt/spi_devices.h"
/* nRF24L01+ 2.4GHz Wireless Transceiver (SPI Mode 0, CSN + CE pins) */

#include "volt/spi.h"
#include "volt/pin.h"
#include "volt/log.h"
#include "volt/task.h"

#define NRF_CONFIG      0x00
#define NRF_EN_AA       0x01
#define NRF_EN_RXADDR   0x02
#define NRF_SETUP_AW    0x03
#define NRF_RF_CH       0x05
#define NRF_RF_SETUP    0x06
#define NRF_STATUS      0x07
#define NRF_RX_ADDR_P0  0x0A
#define NRF_TX_ADDR     0x10
#define NRF_RX_PW_P0    0x11
#define NRF_R_RX_PAYLOAD 0x61
#define NRF_W_TX_PAYLOAD 0xA0
#define NRF_FLUSH_TX    0xE1
#define NRF_FLUSH_RX    0xE2

static uint8_t nrf_read_reg(nrf24l01_t *dev, uint8_t reg)
{
    uint8_t tx[2] = { reg & 0x1F, 0xFF };
    uint8_t rx[2] = {0};
    volt_spi_cs_select(dev->cs_pin);
    volt_spi_transfer(dev->bus_id, tx, rx, 2);
    volt_spi_cs_deselect(dev->cs_pin);
    return rx[1];
}

static void nrf_write_reg(nrf24l01_t *dev, uint8_t reg, uint8_t val)
{
    uint8_t tx[2] = { 0x20 | (reg & 0x1F), val };
    volt_spi_cs_select(dev->cs_pin);
    volt_spi_write(dev->bus_id, tx, 2);
    volt_spi_cs_deselect(dev->cs_pin);
}

volt_error_t nrf24l01_init(nrf24l01_t *dev, uint8_t bus_id,
                           uint8_t cs_pin, uint8_t ce_pin, uint8_t channel)
{
    dev->bus_id = bus_id;
    dev->cs_pin = cs_pin;
    dev->ce_pin = ce_pin;
    dev->channel = channel ? channel : 76;
    dev->payload_size = 32;

    volt_spi_cs_deselect(cs_pin);
    pin_set_output(ce_pin);
    pin_write(ce_pin, 0);

    volt_delay_ms(5);

    // Power up, CRC enabled, PTX mode
    nrf_write_reg(dev, NRF_CONFIG, 0x0E);
    volt_delay_ms(2);

    nrf_write_reg(dev, NRF_EN_AA, 0x01);      // auto-ack on pipe 0
    nrf_write_reg(dev, NRF_EN_RXADDR, 0x01);   // enable pipe 0
    nrf_write_reg(dev, NRF_SETUP_AW, 0x03);    // 5-byte address
    nrf_write_reg(dev, NRF_RF_CH, dev->channel);
    nrf_write_reg(dev, NRF_RF_SETUP, 0x06);    // 1Mbps, 0dBm
    nrf_write_reg(dev, NRF_RX_PW_P0, dev->payload_size);

    // Flush FIFOs
    volt_spi_cs_select(cs_pin);
    uint8_t flush = NRF_FLUSH_TX;
    volt_spi_write(bus_id, &flush, 1);
    volt_spi_cs_deselect(cs_pin);

    volt_spi_cs_select(cs_pin);
    flush = NRF_FLUSH_RX;
    volt_spi_write(bus_id, &flush, 1);
    volt_spi_cs_deselect(cs_pin);

    // Clear status flags
    nrf_write_reg(dev, NRF_STATUS, 0x70);

    log_info("nrf24", "initialized: ch=%d cs=%d ce=%d", dev->channel, cs_pin, ce_pin);
    return VOLT_OK;
}

int nrf24l01_send(nrf24l01_t *dev, const uint8_t *data, uint8_t len)
{
    if (len > 32) len = 32;

    // Write TX payload
    volt_spi_cs_select(dev->cs_pin);
    uint8_t cmd = NRF_W_TX_PAYLOAD;
    volt_spi_write(dev->bus_id, &cmd, 1);
    volt_spi_write(dev->bus_id, data, len);
    volt_spi_cs_deselect(dev->cs_pin);

    // Pulse CE to transmit
    pin_write(dev->ce_pin, 1);
    for (volatile int i = 0; i < 100; i++);  // >10us
    pin_write(dev->ce_pin, 0);

    // Wait for TX complete or timeout
    volt_delay_ms(5);
    uint8_t status = nrf_read_reg(dev, NRF_STATUS);
    nrf_write_reg(dev, NRF_STATUS, 0x70); // clear flags

    return (status & 0x20) ? 1 : 0;  // TX_DS = success
}

int nrf24l01_receive(nrf24l01_t *dev, uint8_t *buf, uint8_t max_len)
{
    // Switch to RX mode
    nrf_write_reg(dev, NRF_CONFIG, 0x0F); // PRX + PWR_UP
    pin_write(dev->ce_pin, 1);
    volt_delay_ms(2);

    uint8_t status = nrf_read_reg(dev, NRF_STATUS);
    if (!(status & 0x40)) {  // RX_DR not set
        pin_write(dev->ce_pin, 0);
        nrf_write_reg(dev, NRF_CONFIG, 0x0E); // back to PTX
        return 0;
    }

    // Read payload
    uint8_t len = (max_len < dev->payload_size) ? max_len : dev->payload_size;
    volt_spi_cs_select(dev->cs_pin);
    uint8_t cmd = NRF_R_RX_PAYLOAD;
    volt_spi_write(dev->bus_id, &cmd, 1);
    volt_spi_read(dev->bus_id, buf, len);
    volt_spi_cs_deselect(dev->cs_pin);

    nrf_write_reg(dev, NRF_STATUS, 0x70); // clear flags
    pin_write(dev->ce_pin, 0);
    nrf_write_reg(dev, NRF_CONFIG, 0x0E); // back to PTX

    return len;
}

int nrf24l01_available(nrf24l01_t *dev)
{
    uint8_t status = nrf_read_reg(dev, NRF_STATUS);
    return (status & 0x40) ? 1 : 0;
}
