#pragma once
#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// HC-SR04 — Ultrasonic Distance (2 GPIO: trigger + echo)
typedef struct { uint8_t trig_pin, echo_pin; } hcsr04_t;
volt_error_t hcsr04_init(hcsr04_t *dev, uint8_t trig, uint8_t echo);
float hcsr04_distance_cm(hcsr04_t *dev);
