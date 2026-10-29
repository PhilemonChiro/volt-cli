#pragma once
#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// DS3231 — Real-Time Clock (addr: 0x68)
typedef struct {
    uint8_t bus_id, addr;
    int seconds, minutes, hours;
    int day, date, month, year;
} ds3231_t;
volt_error_t ds3231_init(ds3231_t *dev, uint8_t bus_id, uint8_t addr);
void ds3231_read_time(ds3231_t *dev);
void ds3231_set_time(ds3231_t *dev, int year, int month, int date,
                     int hours, int minutes, int seconds);
float ds3231_temperature(ds3231_t *dev);
int ds3231_hours(ds3231_t *dev);
int ds3231_minutes(ds3231_t *dev);
int ds3231_seconds(ds3231_t *dev);
int ds3231_year(ds3231_t *dev);
int ds3231_month(ds3231_t *dev);
int ds3231_date(ds3231_t *dev);
