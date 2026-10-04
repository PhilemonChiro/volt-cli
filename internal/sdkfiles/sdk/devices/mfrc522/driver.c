#include "volt/spi_devices.h"
/* MFRC522 RFID 13.56MHz Reader/Writer (SPI, CS active low) */

#include "volt/spi.h"
#include "volt/log.h"
#include "volt/task.h"

#define MFRC522_COMMAND_REG     0x01
#define MFRC522_COMIEN_REG      0x02
#define MFRC522_DIVIEN_REG      0x03
#define MFRC522_COMIRQ_REG      0x04
#define MFRC522_ERROR_REG       0x06
#define MFRC522_FIFO_DATA_REG   0x09
#define MFRC522_FIFO_LEVEL_REG  0x0A
#define MFRC522_CONTROL_REG     0x0C
#define MFRC522_BIT_FRAMING_REG 0x0D
#define MFRC522_MODE_REG        0x11
#define MFRC522_TX_CONTROL_REG  0x14
#define MFRC522_TX_ASK_REG      0x15
#define MFRC522_CRC_RESULT_H    0x21
#define MFRC522_CRC_RESULT_L    0x22
#define MFRC522_TMODE_REG       0x2A
#define MFRC522_TPRESCALER_REG  0x2B
#define MFRC522_TRELOAD_H       0x2C
#define MFRC522_TRELOAD_L       0x2D
#define MFRC522_VERSION_REG     0x37

static uint8_t mfrc522_read_reg(mfrc522_t *dev, uint8_t reg)
{
    uint8_t tx[2] = { ((reg << 1) & 0x7E) | 0x80, 0x00 };
    uint8_t rx[2] = {0};
    volt_spi_cs_select(dev->cs_pin);
    volt_spi_transfer(dev->bus_id, tx, rx, 2);
    volt_spi_cs_deselect(dev->cs_pin);
    return rx[1];
}

static void mfrc522_write_reg(mfrc522_t *dev, uint8_t reg, uint8_t val)
{
    uint8_t tx[2] = { (reg << 1) & 0x7E, val };
    volt_spi_cs_select(dev->cs_pin);
    volt_spi_write(dev->bus_id, tx, 2);
    volt_spi_cs_deselect(dev->cs_pin);
}

volt_error_t mfrc522_init(mfrc522_t *dev, uint8_t bus_id, uint8_t cs_pin)
{
    dev->bus_id = bus_id;
    dev->cs_pin = cs_pin;
    dev->uid_len = 0;
    volt_spi_cs_deselect(cs_pin);

    // Soft reset
    mfrc522_write_reg(dev, MFRC522_COMMAND_REG, 0x0F);
    volt_delay_ms(50);

    // Timer: auto start, prescaler for 25ms timeout
    mfrc522_write_reg(dev, MFRC522_TMODE_REG, 0x80);
    mfrc522_write_reg(dev, MFRC522_TPRESCALER_REG, 0xA9);
    mfrc522_write_reg(dev, MFRC522_TRELOAD_H, 0x03);
    mfrc522_write_reg(dev, MFRC522_TRELOAD_L, 0xE8);

    mfrc522_write_reg(dev, MFRC522_TX_ASK_REG, 0x40);
    mfrc522_write_reg(dev, MFRC522_MODE_REG, 0x3D);

    // Turn on antenna
    uint8_t val = mfrc522_read_reg(dev, MFRC522_TX_CONTROL_REG);
    mfrc522_write_reg(dev, MFRC522_TX_CONTROL_REG, val | 0x03);

    uint8_t version = mfrc522_read_reg(dev, MFRC522_VERSION_REG);
    log_info("mfrc522", "RFID reader v%d.0 on bus %d cs=%d",
             (version == 0x92) ? 2 : 1, bus_id, cs_pin);
    return VOLT_OK;
}

int mfrc522_is_card_present(mfrc522_t *dev)
{
    // Send REQA command
    mfrc522_write_reg(dev, MFRC522_BIT_FRAMING_REG, 0x07);
    uint8_t cmd = 0x26;  // REQA
    mfrc522_write_reg(dev, MFRC522_FIFO_LEVEL_REG, 0x80); // flush FIFO
    mfrc522_write_reg(dev, MFRC522_FIFO_DATA_REG, cmd);
    mfrc522_write_reg(dev, MFRC522_COMMAND_REG, 0x0C); // Transceive
    mfrc522_write_reg(dev, MFRC522_BIT_FRAMING_REG, 0x87); // start send

    volt_delay_ms(10);

    uint8_t err = mfrc522_read_reg(dev, MFRC522_ERROR_REG);
    uint8_t fifo = mfrc522_read_reg(dev, MFRC522_FIFO_LEVEL_REG);

    return (err == 0 && fifo > 0) ? 1 : 0;
}

int mfrc522_read_uid(mfrc522_t *dev)
{
    if (!mfrc522_is_card_present(dev)) return 0;

    // Anti-collision
    mfrc522_write_reg(dev, MFRC522_BIT_FRAMING_REG, 0x00);
    uint8_t cmd[2] = { 0x93, 0x20 };  // SELECT, NVB=2
    mfrc522_write_reg(dev, MFRC522_FIFO_LEVEL_REG, 0x80);
    mfrc522_write_reg(dev, MFRC522_FIFO_DATA_REG, cmd[0]);
    mfrc522_write_reg(dev, MFRC522_FIFO_DATA_REG, cmd[1]);
    mfrc522_write_reg(dev, MFRC522_COMMAND_REG, 0x0C);

    volt_delay_ms(10);

    uint8_t fifo_len = mfrc522_read_reg(dev, MFRC522_FIFO_LEVEL_REG);
    if (fifo_len < 4) return 0;

    dev->uid_len = (fifo_len > 10) ? 10 : fifo_len;
    for (int i = 0; i < dev->uid_len; i++) {
        dev->uid[i] = mfrc522_read_reg(dev, MFRC522_FIFO_DATA_REG);
    }
    return dev->uid_len;
}
