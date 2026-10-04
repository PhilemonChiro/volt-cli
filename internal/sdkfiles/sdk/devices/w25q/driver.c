#include "volt/spi_devices.h"
/* W25Qxx SPI Flash Memory driver (W25Q32/Q64/Q128)
   SPI Mode 0, max 104MHz, CS active low */

#include "volt/spi.h"
#include "volt/log.h"
#include "volt/task.h"
#include <string.h>

#define W25Q_CMD_WRITE_ENABLE   0x06
#define W25Q_CMD_WRITE_DISABLE  0x04
#define W25Q_CMD_READ_STATUS1   0x05
#define W25Q_CMD_READ_DATA      0x03
#define W25Q_CMD_PAGE_PROGRAM   0x02
#define W25Q_CMD_SECTOR_ERASE   0x20
#define W25Q_CMD_BLOCK_ERASE    0xD8
#define W25Q_CMD_CHIP_ERASE     0xC7
#define W25Q_CMD_JEDEC_ID       0x9F
#define W25Q_CMD_POWER_DOWN     0xB9
#define W25Q_CMD_RELEASE_PD     0xAB

static void w25q_wait_busy(w25q_t *dev)
{
    uint8_t status;
    do {
        volt_spi_cs_select(dev->cs_pin);
        uint8_t cmd = W25Q_CMD_READ_STATUS1;
        volt_spi_write(dev->bus_id, &cmd, 1);
        volt_spi_read(dev->bus_id, &status, 1);
        volt_spi_cs_deselect(dev->cs_pin);
    } while (status & 0x01);  // bit 0 = BUSY
}

static void w25q_write_enable(w25q_t *dev)
{
    volt_spi_cs_select(dev->cs_pin);
    uint8_t cmd = W25Q_CMD_WRITE_ENABLE;
    volt_spi_write(dev->bus_id, &cmd, 1);
    volt_spi_cs_deselect(dev->cs_pin);
}

volt_error_t w25q_init(w25q_t *dev, uint8_t bus_id, uint8_t cs_pin)
{
    dev->bus_id = bus_id;
    dev->cs_pin = cs_pin;

    // Init CS pin high (deselected)
    volt_spi_cs_deselect(cs_pin);

    // Release from power-down
    volt_spi_cs_select(cs_pin);
    uint8_t cmd = W25Q_CMD_RELEASE_PD;
    volt_spi_write(bus_id, &cmd, 1);
    volt_spi_cs_deselect(cs_pin);
    volt_delay_ms(1);

    // Read JEDEC ID
    uint8_t id[3];
    volt_spi_cs_select(cs_pin);
    cmd = W25Q_CMD_JEDEC_ID;
    volt_spi_write(bus_id, &cmd, 1);
    volt_spi_read(bus_id, id, 3);
    volt_spi_cs_deselect(cs_pin);

    dev->manufacturer = id[0];
    dev->device_id = id[2];

    // Determine capacity from device ID
    switch (id[2]) {
        case 0x15: dev->capacity = 2 * 1024 * 1024; break;  // W25Q16 (2MB)
        case 0x16: dev->capacity = 4 * 1024 * 1024; break;  // W25Q32 (4MB)
        case 0x17: dev->capacity = 8 * 1024 * 1024; break;  // W25Q64 (8MB)
        case 0x18: dev->capacity = 16 * 1024 * 1024; break; // W25Q128 (16MB)
        default:   dev->capacity = 0; break;
    }

    log_info("w25q", "flash detected: mfr=0x%02X dev=0x%02X capacity=%luKB",
             id[0], id[2], (unsigned long)(dev->capacity / 1024));
    return (dev->manufacturer == 0xEF) ? VOLT_OK : VOLT_ERR_INTERNAL;
}

volt_error_t w25q_read(w25q_t *dev, uint32_t addr, uint8_t *buf, uint16_t len)
{
    volt_spi_cs_select(dev->cs_pin);
    uint8_t cmd[4] = {
        W25Q_CMD_READ_DATA,
        (uint8_t)(addr >> 16),
        (uint8_t)(addr >> 8),
        (uint8_t)(addr),
    };
    volt_spi_write(dev->bus_id, cmd, 4);
    volt_spi_read(dev->bus_id, buf, len);
    volt_spi_cs_deselect(dev->cs_pin);
    return VOLT_OK;
}

volt_error_t w25q_write_page(w25q_t *dev, uint32_t addr, const uint8_t *data, uint16_t len)
{
    if (len > 256) len = 256;  // page size limit

    w25q_write_enable(dev);

    volt_spi_cs_select(dev->cs_pin);
    uint8_t cmd[4] = {
        W25Q_CMD_PAGE_PROGRAM,
        (uint8_t)(addr >> 16),
        (uint8_t)(addr >> 8),
        (uint8_t)(addr),
    };
    volt_spi_write(dev->bus_id, cmd, 4);
    volt_spi_write(dev->bus_id, data, len);
    volt_spi_cs_deselect(dev->cs_pin);

    w25q_wait_busy(dev);
    return VOLT_OK;
}

volt_error_t w25q_erase_sector(w25q_t *dev, uint32_t addr)
{
    w25q_write_enable(dev);

    volt_spi_cs_select(dev->cs_pin);
    uint8_t cmd[4] = {
        W25Q_CMD_SECTOR_ERASE,
        (uint8_t)(addr >> 16),
        (uint8_t)(addr >> 8),
        (uint8_t)(addr),
    };
    volt_spi_write(dev->bus_id, cmd, 4);
    volt_spi_cs_deselect(dev->cs_pin);

    w25q_wait_busy(dev);
    return VOLT_OK;
}

volt_error_t w25q_erase_chip(w25q_t *dev)
{
    w25q_write_enable(dev);

    volt_spi_cs_select(dev->cs_pin);
    uint8_t cmd = W25Q_CMD_CHIP_ERASE;
    volt_spi_write(dev->bus_id, &cmd, 1);
    volt_spi_cs_deselect(dev->cs_pin);

    w25q_wait_busy(dev);
    return VOLT_OK;
}
