#pragma once
#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// BME280 — Temperature, Humidity, Pressure (addr: 0x76/0x77)
typedef struct {
    uint8_t bus_id, addr;
    uint16_t dig_T1;
    int16_t  dig_T2, dig_T3;
    uint16_t dig_P1;
    int16_t  dig_P2, dig_P3, dig_P4, dig_P5, dig_P6, dig_P7, dig_P8, dig_P9;
    uint8_t  dig_H1, dig_H3;
    int16_t  dig_H2, dig_H4, dig_H5;
    int8_t   dig_H6;
    int32_t  t_fine;
} bme280_t;
volt_error_t bme280_init(bme280_t *dev, uint8_t bus_id, uint8_t addr);
float bme280_temperature(bme280_t *dev);
float bme280_humidity(bme280_t *dev);
float bme280_pressure(bme280_t *dev);
