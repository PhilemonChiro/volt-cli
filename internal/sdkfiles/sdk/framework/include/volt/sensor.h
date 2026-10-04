#ifndef VOLT_SENSOR_H
#define VOLT_SENSOR_H

#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// ============================================================
// BME280 — Temperature, Humidity, Pressure (addr: 0x76/0x77)
// ============================================================
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

// ============================================================
// AHT20 — Temperature + Humidity (addr: 0x38)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
    float last_temperature, last_humidity;
} aht20_t;

volt_error_t aht20_init(aht20_t *dev, uint8_t bus_id, uint8_t addr);
float aht20_temperature(aht20_t *dev);
float aht20_humidity(aht20_t *dev);

// ============================================================
// SHT31 — High-accuracy Temp + Humidity (addr: 0x44/0x45)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
    float last_temperature, last_humidity;
} sht31_t;

volt_error_t sht31_init(sht31_t *dev, uint8_t bus_id, uint8_t addr);
float sht31_temperature(sht31_t *dev);
float sht31_humidity(sht31_t *dev);

// ============================================================
// MPU6050 — 6-axis IMU (addr: 0x68/0x69)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
    float accel_x, accel_y, accel_z;  // g
    float gyro_x, gyro_y, gyro_z;    // °/s
    float temperature;                // °C
} mpu6050_t;

volt_error_t mpu6050_init(mpu6050_t *dev, uint8_t bus_id, uint8_t addr);
float mpu6050_accel_x(mpu6050_t *dev);
float mpu6050_accel_y(mpu6050_t *dev);
float mpu6050_accel_z(mpu6050_t *dev);
float mpu6050_gyro_x(mpu6050_t *dev);
float mpu6050_gyro_y(mpu6050_t *dev);
float mpu6050_gyro_z(mpu6050_t *dev);
float mpu6050_temperature(mpu6050_t *dev);

// ============================================================
// INA219 — Current/Voltage/Power monitor (addr: 0x40-0x4F)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
    float current_lsb;  // amps per bit (set during calibration)
} ina219_t;

volt_error_t ina219_init(ina219_t *dev, uint8_t bus_id, uint8_t addr);
float ina219_voltage(ina219_t *dev);      // bus voltage (V)
float ina219_current(ina219_t *dev);      // current (A)
float ina219_power(ina219_t *dev);        // power (W)
float ina219_shunt_voltage(ina219_t *dev); // shunt voltage (V)

// ============================================================
// DS3231 — Real-Time Clock (addr: 0x68)
// ============================================================
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

// ============================================================
// BH1750 — Light Sensor (addr: 0x23/0x5C)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
} bh1750_t;

volt_error_t bh1750_init(bh1750_t *dev, uint8_t bus_id, uint8_t addr);
float bh1750_lux(bh1750_t *dev);

// ============================================================
// VEML7700 — High-accuracy Light Sensor (addr: 0x10)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
} veml7700_t;

volt_error_t veml7700_init(veml7700_t *dev, uint8_t bus_id, uint8_t addr);
float veml7700_lux(veml7700_t *dev);
float veml7700_white(veml7700_t *dev);

// ============================================================
// SSD1306 — OLED Display 128x64 (addr: 0x3C/0x3D)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
    uint8_t buffer[1024];
} ssd1306_t;

volt_error_t ssd1306_init(ssd1306_t *dev, uint8_t bus_id, uint8_t addr);
void ssd1306_clear(ssd1306_t *dev);
void ssd1306_text(ssd1306_t *dev, int x, int y, const char *text);
void ssd1306_show(ssd1306_t *dev);


// ============================================================
// BMP180 — Barometric Pressure + Temperature (addr: 0x77)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
    int16_t AC1, AC2, AC3;
    uint16_t AC4, AC5, AC6;
    int16_t B1, B2, MB, MC, MD;
    int32_t B5;
} bmp180_t;
volt_error_t bmp180_init(bmp180_t *dev, uint8_t bus_id, uint8_t addr);
float bmp180_temperature(bmp180_t *dev);
float bmp180_pressure(bmp180_t *dev);

// ============================================================
// ADS1115 — 16-bit 4-channel ADC (addr: 0x48-0x4B)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
    float gain_voltage;
} ads1115_t;
volt_error_t ads1115_init(ads1115_t *dev, uint8_t bus_id, uint8_t addr);
int ads1115_read_raw(ads1115_t *dev, uint8_t channel);
float ads1115_voltage(ads1115_t *dev, uint8_t channel);

// ============================================================
// PCA9685 — 16-channel PWM/Servo controller (addr: 0x40)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
    float frequency;
} pca9685_t;
volt_error_t pca9685_init(pca9685_t *dev, uint8_t bus_id, uint8_t addr);
void pca9685_set_pwm(pca9685_t *dev, uint8_t channel, uint16_t on, uint16_t off);
void pca9685_set_servo(pca9685_t *dev, uint8_t channel, int angle);
void pca9685_set_duty(pca9685_t *dev, uint8_t channel, uint8_t duty);

// ============================================================
// PCF8574 — I2C GPIO Expander (addr: 0x20-0x27)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
    uint8_t state;
} pcf8574_t;
volt_error_t pcf8574_init(pcf8574_t *dev, uint8_t bus_id, uint8_t addr);
void pcf8574_write(pcf8574_t *dev, uint8_t value);
uint8_t pcf8574_read(pcf8574_t *dev);
void pcf8574_set_pin(pcf8574_t *dev, uint8_t pin, uint8_t level);

// ============================================================
// MLX90614 — Non-contact IR Temperature (addr: 0x5A)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
} mlx90614_t;
volt_error_t mlx90614_init(mlx90614_t *dev, uint8_t bus_id, uint8_t addr);
float mlx90614_ambient(mlx90614_t *dev);
float mlx90614_object(mlx90614_t *dev);

// ============================================================
// MCP4725 — 12-bit DAC (addr: 0x60/0x61)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
} mcp4725_t;
volt_error_t mcp4725_init(mcp4725_t *dev, uint8_t bus_id, uint8_t addr);
void mcp4725_write(mcp4725_t *dev, uint16_t value);
void mcp4725_set_voltage(mcp4725_t *dev, float voltage, float vref);

// ============================================================
// MAX30102 — Pulse Oximeter + Heart Rate (addr: 0x57)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
    uint32_t ir_value, red_value;
    float temperature;
} max30102_t;
volt_error_t max30102_init(max30102_t *dev, uint8_t bus_id, uint8_t addr);
int max30102_red(max30102_t *dev);
int max30102_ir(max30102_t *dev);
float max30102_temperature(max30102_t *dev);
int max30102_is_finger_present(max30102_t *dev);

// ============================================================
// MAX30100 — Pulse Oximeter (older, 16-bit, addr: 0x57)
// ============================================================
typedef struct {
    uint8_t bus_id, addr;
    uint16_t ir_value, red_value;
    float temperature;
} max30100_t;
volt_error_t max30100_init(max30100_t *dev, uint8_t bus_id, uint8_t addr);
int max30100_red(max30100_t *dev);
int max30100_ir(max30100_t *dev);
float max30100_temperature(max30100_t *dev);
int max30100_is_finger_present(max30100_t *dev);

// (inserted before #endif by removing it first and re-adding)

// ============================================================
// HD44780 LCD 16x2/20x4 via I2C (addr: 0x27)
// ============================================================
typedef struct { uint8_t bus_id, addr, cols, rows, backlight; } hd44780_t;
volt_error_t hd44780_init(hd44780_t *dev, uint8_t bus_id, uint8_t addr, uint8_t cols, uint8_t rows);
void hd44780_clear(hd44780_t *dev);
void hd44780_set_cursor(hd44780_t *dev, uint8_t col, uint8_t row);
void hd44780_print(hd44780_t *dev, const char *text);
void hd44780_backlight(hd44780_t *dev, uint8_t on);
void hd44780_create_char(hd44780_t *dev, uint8_t slot, uint8_t r0, uint8_t r1, uint8_t r2, uint8_t r3, uint8_t r4, uint8_t r5, uint8_t r6, uint8_t r7);
void hd44780_write_char(hd44780_t *dev, uint8_t code);
void hd44780_reinit(hd44780_t *dev);

// ============================================================
// BME680 — Gas + Temp + Humidity + Pressure (addr: 0x76/0x77)
// ============================================================
typedef struct { uint8_t bus_id, addr; float last_temp, last_hum, last_pres; uint32_t last_gas; } bme680_t;
volt_error_t bme680_init(bme680_t *dev, uint8_t bus_id, uint8_t addr);
float bme680_temperature(bme680_t *dev);
float bme680_humidity(bme680_t *dev);
float bme680_pressure(bme680_t *dev);
int bme680_gas(bme680_t *dev);

// ============================================================
// APDS9960 — Gesture/Proximity/RGB (addr: 0x39)
// ============================================================
typedef struct { uint8_t bus_id, addr; } apds9960_t;
volt_error_t apds9960_init(apds9960_t *dev, uint8_t bus_id, uint8_t addr);
int apds9960_proximity(apds9960_t *dev);
int apds9960_red(apds9960_t *dev);
int apds9960_green(apds9960_t *dev);
int apds9960_blue(apds9960_t *dev);
int apds9960_clear(apds9960_t *dev);

// ============================================================
// QMC5883L — Compass/Magnetometer (addr: 0x0D)
// ============================================================
typedef struct { uint8_t bus_id, addr; float x, y, z; } qmc5883l_t;
volt_error_t qmc5883l_init(qmc5883l_t *dev, uint8_t bus_id, uint8_t addr);
float qmc5883l_x(qmc5883l_t *dev);
float qmc5883l_y(qmc5883l_t *dev);
float qmc5883l_z(qmc5883l_t *dev);
float qmc5883l_heading(qmc5883l_t *dev);

// ============================================================
// TCA9548A — I2C Multiplexer (addr: 0x70)
// ============================================================
typedef struct { uint8_t bus_id, addr, current_channel; } tca9548a_t;
volt_error_t tca9548a_init(tca9548a_t *dev, uint8_t bus_id, uint8_t addr);
void tca9548a_select(tca9548a_t *dev, uint8_t channel);
void tca9548a_disable(tca9548a_t *dev);

// ============================================================
// HTU21D — Temperature + Humidity (addr: 0x40)
// ============================================================
typedef struct { uint8_t bus_id, addr; } htu21d_t;
volt_error_t htu21d_init(htu21d_t *dev, uint8_t bus_id, uint8_t addr);
float htu21d_temperature(htu21d_t *dev);
float htu21d_humidity(htu21d_t *dev);

// ============================================================
// MS5611 — High-res Barometric Pressure (addr: 0x77)
// ============================================================
typedef struct { uint8_t bus_id, addr; uint16_t C[7]; int32_t dT, TEMP; } ms5611_t;
volt_error_t ms5611_init(ms5611_t *dev, uint8_t bus_id, uint8_t addr);
float ms5611_temperature(ms5611_t *dev);
float ms5611_pressure(ms5611_t *dev);

// ============================================================
// MAX7219 — LED Matrix/7-Segment (SPI)
// ============================================================
typedef struct { uint8_t bus_id, cs_pin, num_devices; } max7219_t;
volt_error_t max7219_init(max7219_t *dev, uint8_t bus_id, uint8_t cs_pin);
void max7219_set_row(max7219_t *dev, uint8_t row, uint8_t data);
void max7219_set_intensity(max7219_t *dev, uint8_t level);
void max7219_clear(max7219_t *dev);
void max7219_set_digit(max7219_t *dev, uint8_t pos, uint8_t value, uint8_t dp);








// hc05 (installed library — generated header)
typedef struct { uint8_t uart_id; } hc05_t;
volt_error_t hc05_init(hc05_t *dev, uint8_t uart_id);
volt_error_t hc05_send_data(hc05_t *dev, int data);
int hc05_receive_data(hc05_t *dev, char * buffer, uint16_t max_len);
volt_error_t hc05_send_at_command(hc05_t *dev, const char * command);
int32_t hc05_read_at_response(hc05_t *dev, char * response, uint16_t max_len);
volt_error_t hc05_set_baud_rate(hc05_t *dev, int baud_rate);
volt_error_t hc05_set_name(hc05_t *dev, const char * name);
volt_error_t hc05_set_password(hc05_t *dev, const char * password);

// ============================================================
// GY-85 9-DoF board: ADXL345 (0x53) + ITG3205 (0x68) + HMC5883L (0x1E)
// ============================================================
typedef struct { uint8_t bus_id, addr; float x, y, z; } adxl345_t;
volt_error_t adxl345_init(adxl345_t *dev, uint8_t bus_id, uint8_t addr);
float adxl345_x(adxl345_t *dev);
float adxl345_y(adxl345_t *dev);
float adxl345_z(adxl345_t *dev);
float adxl345_magnitude(adxl345_t *dev);

typedef struct { uint8_t bus_id, addr; float x, y, z; } itg3205_t;
volt_error_t itg3205_init(itg3205_t *dev, uint8_t bus_id, uint8_t addr);
float itg3205_x(itg3205_t *dev);
float itg3205_y(itg3205_t *dev);
float itg3205_z(itg3205_t *dev);
float itg3205_magnitude(itg3205_t *dev);

typedef struct { uint8_t bus_id, addr; float x, y, z; } hmc5883l_t;
volt_error_t hmc5883l_init(hmc5883l_t *dev, uint8_t bus_id, uint8_t addr);
float hmc5883l_x(hmc5883l_t *dev);
float hmc5883l_y(hmc5883l_t *dev);
float hmc5883l_z(hmc5883l_t *dev);
float hmc5883l_heading(hmc5883l_t *dev);

// ============================================================
// SIM800L — GSM/GPRS modem (UART)
// ============================================================
typedef struct { uint8_t uart_id; } sim800l_t;
volt_error_t sim800l_init(sim800l_t *dev, uint8_t uart_id);
bool sim800l_ready(sim800l_t *dev);
bool sim800l_registered(sim800l_t *dev);
int32_t sim800l_signal(sim800l_t *dev);
bool sim800l_send_sms(sim800l_t *dev, const char *number, const char *text);

// ============================================================
// SIM7000 — LTE-M/NB-IoT/GSM modem (UART): SMS + HTTP over GPRS
// ============================================================
typedef struct {
    uint8_t uart_id;
    int32_t last_ref;          // reference of the last SMS (-1 none)
    int32_t receipt;           // 1 delivered, 0 pending, -1 failed
    bool alive;
    int16_t pwrkey;            // PWRKEY GPIO, -1 if not wired
    bool pwr_active_high;
    uint32_t last_reg_ms;      // last time it was seen registered
} sim7000_t;
volt_error_t sim7000_init(sim7000_t *dev, uint8_t uart_id);
void sim7000_set_pwrkey(sim7000_t *dev, int32_t pin, bool active_high);
bool sim7000_reset(sim7000_t *dev);
bool sim7000_power_cycle(sim7000_t *dev);
bool sim7000_recover(sim7000_t *dev);
int32_t sim7000_maintain(sim7000_t *dev);
bool sim7000_ready(sim7000_t *dev);
bool sim7000_registered(sim7000_t *dev);
int32_t sim7000_network_status(sim7000_t *dev);
int32_t sim7000_signal(sim7000_t *dev);
int32_t sim7000_signal_dbm(sim7000_t *dev);
const char *sim7000_operator_name(sim7000_t *dev);
int32_t sim7000_voltage(sim7000_t *dev);
int32_t sim7000_network_time(sim7000_t *dev);
bool sim7000_send_sms(sim7000_t *dev, const char *number, const char *text);
int32_t sim7000_check_receipt(sim7000_t *dev);
int32_t sim7000_http_post(sim7000_t *dev, const char *url, const char *body);
const char *sim7000_ussd(sim7000_t *dev, const char *code);
void sim7000_allow_number(sim7000_t *dev, const char *number);
bool sim7000_next_sms(sim7000_t *dev);
const char *sim7000_sms_from(sim7000_t *dev);
const char *sim7000_sms_text(sim7000_t *dev);
bool sim7000_sms_allowed(sim7000_t *dev);
const char *sim7000_sms_command(sim7000_t *dev);
bool sim7000_sms_command_is(sim7000_t *dev, const char *cmd);
int32_t sim7000_sms_arg_int(sim7000_t *dev);
const char *sim7000_sms_arg(sim7000_t *dev);
bool sim7000_sms_arg_is(sim7000_t *dev, const char *word);
void sim7000_load_alert_number(sim7000_t *dev, const char *fallback);
const char *sim7000_alert_number(sim7000_t *dev);
bool sim7000_set_alert_number_from_sms(sim7000_t *dev);
void sim7000_load_whitelist(sim7000_t *dev);
int32_t sim7000_whitelist_add_from_sms(sim7000_t *dev);
int32_t sim7000_whitelist_remove_from_sms(sim7000_t *dev);
const char *sim7000_whitelist_text(sim7000_t *dev);

#endif // VOLT_SENSOR_H
