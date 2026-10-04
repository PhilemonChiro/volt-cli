#ifndef VOLT_SPI_DEVICES_H
#define VOLT_SPI_DEVICES_H

#include <stdint.h>
#include "volt/error.h"

// ============================================================
// W25Qxx — SPI Flash Memory (W25Q32/Q64/Q128)
// ============================================================
typedef struct {
    uint8_t bus_id;
    uint8_t cs_pin;
    uint8_t manufacturer;
    uint8_t device_id;
    uint32_t capacity;
} w25q_t;

volt_error_t w25q_init(w25q_t *dev, uint8_t bus_id, uint8_t cs_pin);
volt_error_t w25q_read(w25q_t *dev, uint32_t addr, uint8_t *buf, uint16_t len);
volt_error_t w25q_write_page(w25q_t *dev, uint32_t addr, const uint8_t *data, uint16_t len);
volt_error_t w25q_erase_sector(w25q_t *dev, uint32_t addr);
volt_error_t w25q_erase_chip(w25q_t *dev);

// ============================================================
// MAX31855 — Thermocouple Temperature (read-only)
// ============================================================
typedef struct {
    uint8_t bus_id;
    uint8_t cs_pin;
    float thermocouple_temp;
    float internal_temp;
    uint8_t fault;
} max31855_t;

volt_error_t max31855_init(max31855_t *dev, uint8_t bus_id, uint8_t cs_pin);
float max31855_temperature(max31855_t *dev);  // thermocouple temp (°C)
float max31855_internal(max31855_t *dev);      // cold junction temp (°C)
int max31855_is_fault(max31855_t *dev);        // 1 if fault detected

// ============================================================
// MCP3008 — 8-Channel 10-bit ADC
// ============================================================
typedef struct {
    uint8_t bus_id;
    uint8_t cs_pin;
} mcp3008_t;

volt_error_t mcp3008_init(mcp3008_t *dev, uint8_t bus_id, uint8_t cs_pin);
int mcp3008_read(mcp3008_t *dev, uint8_t channel);  // returns 0-1023


// ============================================================
// MFRC522 — RFID 13.56MHz (SPI)
// ============================================================
typedef struct { uint8_t bus_id, cs_pin; uint8_t uid[10]; uint8_t uid_len; } mfrc522_t;
volt_error_t mfrc522_init(mfrc522_t *dev, uint8_t bus_id, uint8_t cs_pin);
int mfrc522_is_card_present(mfrc522_t *dev);
int mfrc522_read_uid(mfrc522_t *dev);

// ============================================================
// nRF24L01+ — 2.4GHz Wireless (SPI + CE pin)
// ============================================================
typedef struct { uint8_t bus_id, cs_pin, ce_pin, channel, payload_size; } nrf24l01_t;
volt_error_t nrf24l01_init(nrf24l01_t *dev, uint8_t bus_id, uint8_t cs_pin, uint8_t ce_pin, uint8_t channel);
int nrf24l01_send(nrf24l01_t *dev, const uint8_t *data, uint8_t len);
int nrf24l01_receive(nrf24l01_t *dev, uint8_t *buf, uint8_t max_len);
int nrf24l01_available(nrf24l01_t *dev);

// ============================================================
// MAX7219 — LED Matrix/7-Segment (SPI)
// ============================================================
/* Already declared in sensor.h */

#endif // VOLT_SPI_DEVICES_H
