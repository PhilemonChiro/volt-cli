/* DS3231 Real-Time Clock driver (I2C address: 0x68) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"

static uint8_t bcd_to_dec(uint8_t bcd) { return (bcd >> 4) * 10 + (bcd & 0x0F); }
static uint8_t dec_to_bcd(uint8_t dec) { return ((dec / 10) << 4) | (dec % 10); }

volt_error_t ds3231_init(ds3231_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x68;

    // Ensure oscillator is running (clear EOSC bit in control register)
    volt_i2c_write_reg(bus_id, dev->addr, 0x0E, 0x00);
    // Clear oscillator stop flag
    uint8_t status = volt_i2c_read_reg(bus_id, dev->addr, 0x0F);
    volt_i2c_write_reg(bus_id, dev->addr, 0x0F, status & 0x7F);

    log_info("ds3231", "initialized at bus %d addr 0x%02X", bus_id, dev->addr);
    return VOLT_OK;
}

void ds3231_read_time(ds3231_t *dev)
{
    uint8_t data[7];
    uint8_t reg = 0x00;
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, data, 7);

    dev->seconds = bcd_to_dec(data[0]);
    dev->minutes = bcd_to_dec(data[1]);
    dev->hours   = bcd_to_dec(data[2] & 0x3F);  // mask 12/24hr bit
    dev->day     = bcd_to_dec(data[3]);
    dev->date    = bcd_to_dec(data[4]);
    dev->month   = bcd_to_dec(data[5] & 0x1F);
    dev->year    = bcd_to_dec(data[6]) + 2000;
}

void ds3231_set_time(ds3231_t *dev, int year, int month, int date,
                     int hours, int minutes, int seconds)
{
    uint8_t data[8] = {
        0x00,  // register address
        dec_to_bcd(seconds),
        dec_to_bcd(minutes),
        dec_to_bcd(hours),
        0x01,  // day of week (unused)
        dec_to_bcd(date),
        dec_to_bcd(month),
        dec_to_bcd(year - 2000),
    };
    volt_i2c_write(dev->bus_id, dev->addr, data, 8);
}

float ds3231_temperature(ds3231_t *dev)
{
    uint8_t data[2];
    uint8_t reg = 0x11;
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, data, 2);
    return (float)(int8_t)data[0] + (float)(data[1] >> 6) * 0.25f;
}

int ds3231_hours(ds3231_t *dev)   { ds3231_read_time(dev); return dev->hours; }
int ds3231_minutes(ds3231_t *dev) { ds3231_read_time(dev); return dev->minutes; }
int ds3231_seconds(ds3231_t *dev) { ds3231_read_time(dev); return dev->seconds; }
int ds3231_year(ds3231_t *dev)    { ds3231_read_time(dev); return dev->year; }
int ds3231_month(ds3231_t *dev)   { ds3231_read_time(dev); return dev->month; }
int ds3231_date(ds3231_t *dev)    { ds3231_read_time(dev); return dev->date; }
