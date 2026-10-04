#ifndef VOLT_SERVO_H
#define VOLT_SERVO_H

#include <stdint.h>
#include "volt/error.h"

// ============================================================
// Easing types — control how the servo interpolates between angles.
// Linear moves at constant speed. Others shape the motion curve.
// ============================================================

#define SERVO_LINEAR          0
#define SERVO_EASE_IN         1   // slow start, fast end (quadratic)
#define SERVO_EASE_OUT        2   // fast start, slow end (quadratic)
#define SERVO_EASE_IN_OUT     3   // slow start and end   (quadratic)
#define SERVO_EASE_IN_CUBIC   4
#define SERVO_EASE_OUT_CUBIC  5
#define SERVO_EASE_IN_OUT_CUBIC 6
#define SERVO_EASE_IN_SINE    7
#define SERVO_EASE_OUT_SINE   8
#define SERVO_EASE_IN_OUT_SINE 9
#define SERVO_EASE_BOUNCE     10  // bounces at the end
#define SERVO_EASE_BACK       11  // overshoots slightly, then settles
#define SERVO_EASE_ELASTIC    12  // spring-like oscillation at the end

// ============================================================
// Servo presets — common pulse ranges for popular servos.
// Default (500-2500us) works for most hobby servos.
// ============================================================

#define SERVO_DEFAULT_MIN_US  500
#define SERVO_DEFAULT_MAX_US  2500

// ============================================================
// Setup
// ============================================================

// Attach a servo on a GPIO pin. Configures 50Hz PWM internally.
// Default pulse range: 500-2500us (works for SG90, MG90S, MG996R, etc.)
volt_error_t servo_attach(uint8_t pin);

// Attach with custom pulse range (for non-standard servos).
// min_us/max_us define the pulse width at 0 and max_angle degrees.
volt_error_t servo_attach_range(uint8_t pin, uint16_t min_us, uint16_t max_us);

// Detach servo — stops PWM signal, releases the pin.
// Servo will go limp (no holding torque).
volt_error_t servo_detach(uint8_t pin);

// ============================================================
// Basic control
// ============================================================

// Move to an angle instantly (0-180 degrees).
volt_error_t servo_write(uint8_t pin, float angle);

// Set exact pulse width in microseconds (bypasses angle mapping).
volt_error_t servo_write_us(uint8_t pin, uint16_t microseconds);

// Read the last written angle.
float servo_read(uint8_t pin);

// ============================================================
// Smooth movement with easing
// ============================================================

// Move smoothly to target angle over duration_ms using an easing curve.
// Blocks for the duration. Use SERVO_LINEAR .. SERVO_EASE_ELASTIC.
volt_error_t servo_ease_to(uint8_t pin, float angle,
                           uint32_t duration_ms, uint8_t easing);

// Sweep continuously between two angles over duration_ms (one way).
// Blocks for the duration.
volt_error_t servo_sweep(uint8_t pin, float from_angle, float to_angle,
                         uint32_t duration_ms);

// Sweep with easing curve applied.
volt_error_t servo_sweep_ease(uint8_t pin, float from_angle, float to_angle,
                              uint32_t duration_ms, uint8_t easing);

// ============================================================
// Multi-servo synchronized movement
// ============================================================

// Begin buffering moves. Calls to servo_sync_move() are queued
// until servo_sync_go() executes them all simultaneously.
void servo_sync_begin(void);

// Queue a move: pin will ease to angle over duration_ms.
// Does NOT start moving yet — call servo_sync_go() to execute.
volt_error_t servo_sync_move(uint8_t pin, float angle,
                             uint32_t duration_ms, uint8_t easing);

// Execute all queued moves simultaneously. Blocks until the
// longest move completes. All servos finish at the same time
// (shorter moves use their full duration).
volt_error_t servo_sync_go(void);

// ============================================================
// Utility
// ============================================================

// Map angle (0-180) to pulse width in microseconds for this pin's range.
uint16_t servo_angle_to_us(uint8_t pin, float angle);

// Set maximum angle (default 180). Use 270 for 270-degree servos.
volt_error_t servo_set_max_angle(uint8_t pin, float max_angle);

#endif // VOLT_SERVO_H
