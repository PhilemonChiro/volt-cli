#pragma once
#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// MPU6050 — 6-axis IMU (addr: 0x68/0x69)
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
