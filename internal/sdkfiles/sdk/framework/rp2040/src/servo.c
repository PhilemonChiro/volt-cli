#include <stdbool.h>
#include <stdint.h>
#include <math.h>
#include "volt/servo.h"
#include "volt/log.h"
#include "pico/stdlib.h"
#include "hardware/pwm.h"

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

// ============================================================
// Internal state
// ============================================================

#define MAX_SERVOS 16
#define SERVO_FREQ_HZ 50

typedef struct {
    uint8_t pin;
    bool attached;
    uint16_t min_us;
    uint16_t max_us;
    float max_angle;
    float current_angle;
    uint slice;
    uint channel;
    uint16_t wrap_val;
} servo_state_t;

static servo_state_t servos[MAX_SERVOS];
static uint8_t servo_count = 0;

// Sync buffer
#define MAX_SYNC_MOVES 8
typedef struct {
    uint8_t servo_idx;
    float target_angle;
    uint32_t duration_ms;
    uint8_t easing;
} sync_move_t;

static sync_move_t sync_buffer[MAX_SYNC_MOVES];
static uint8_t sync_count = 0;
static bool sync_active = false;

// ============================================================
// Easing functions — t in [0,1] → [0,1]
// ============================================================

static float ease_linear(float t) { return t; }
static float ease_in_quad(float t) { return t * t; }
static float ease_out_quad(float t) { return t * (2.0f - t); }

static float ease_in_out_quad(float t)
{
    if (t < 0.5f) return 2.0f * t * t;
    return -1.0f + (4.0f - 2.0f * t) * t;
}

static float ease_in_cubic(float t) { return t * t * t; }

static float ease_out_cubic(float t)
{
    float f = t - 1.0f;
    return f * f * f + 1.0f;
}

static float ease_in_out_cubic(float t)
{
    if (t < 0.5f) return 4.0f * t * t * t;
    float f = 2.0f * t - 2.0f;
    return 0.5f * f * f * f + 1.0f;
}

static float ease_in_sine(float t)
{
    return 1.0f - cosf(t * (float)M_PI / 2.0f);
}

static float ease_out_sine(float t)
{
    return sinf(t * (float)M_PI / 2.0f);
}

static float ease_in_out_sine(float t)
{
    return 0.5f * (1.0f - cosf(t * (float)M_PI));
}

static float ease_out_bounce(float t)
{
    if (t < 1.0f / 2.75f) {
        return 7.5625f * t * t;
    } else if (t < 2.0f / 2.75f) {
        t -= 1.5f / 2.75f;
        return 7.5625f * t * t + 0.75f;
    } else if (t < 2.5f / 2.75f) {
        t -= 2.25f / 2.75f;
        return 7.5625f * t * t + 0.9375f;
    } else {
        t -= 2.625f / 2.75f;
        return 7.5625f * t * t + 0.984375f;
    }
}

static float ease_out_back(float t)
{
    const float c = 1.70158f;
    float f = t - 1.0f;
    return 1.0f + (c + 1.0f) * f * f * f + c * f * f;
}

static float ease_out_elastic(float t)
{
    if (t <= 0.0f) return 0.0f;
    if (t >= 1.0f) return 1.0f;
    return powf(2.0f, -10.0f * t) * sinf((t - 0.075f) * (2.0f * (float)M_PI) / 0.3f) + 1.0f;
}

static float apply_easing(float t, uint8_t easing)
{
    switch (easing) {
        case SERVO_LINEAR:            return ease_linear(t);
        case SERVO_EASE_IN:           return ease_in_quad(t);
        case SERVO_EASE_OUT:          return ease_out_quad(t);
        case SERVO_EASE_IN_OUT:       return ease_in_out_quad(t);
        case SERVO_EASE_IN_CUBIC:     return ease_in_cubic(t);
        case SERVO_EASE_OUT_CUBIC:    return ease_out_cubic(t);
        case SERVO_EASE_IN_OUT_CUBIC: return ease_in_out_cubic(t);
        case SERVO_EASE_IN_SINE:      return ease_in_sine(t);
        case SERVO_EASE_OUT_SINE:     return ease_out_sine(t);
        case SERVO_EASE_IN_OUT_SINE:  return ease_in_out_sine(t);
        case SERVO_EASE_BOUNCE:       return ease_out_bounce(t);
        case SERVO_EASE_BACK:         return ease_out_back(t);
        case SERVO_EASE_ELASTIC:      return ease_out_elastic(t);
        default:                      return ease_linear(t);
    }
}

// ============================================================
// Internal helpers
// ============================================================

static int find_servo(uint8_t pin)
{
    for (int i = 0; i < servo_count; i++) {
        if (servos[i].pin == pin && servos[i].attached) return i;
    }
    return -1;
}

static void set_pulse_us(servo_state_t *s, uint16_t pulse_us)
{
    // Convert microseconds to PWM level.
    // At 50Hz, period = 20000us. wrap_val counts = 20000us.
    // level = pulse_us / 20000 * wrap_val
    uint16_t level = (uint16_t)((float)pulse_us / 20000.0f * (float)s->wrap_val);
    pwm_set_chan_level(s->slice, s->channel, level);
}

// ============================================================
// Public API
// ============================================================

volt_error_t servo_attach(uint8_t pin)
{
    return servo_attach_range(pin, SERVO_DEFAULT_MIN_US, SERVO_DEFAULT_MAX_US);
}

volt_error_t servo_attach_range(uint8_t pin, uint16_t min_us, uint16_t max_us)
{
    if (pin >= 30) return VOLT_ERR_INVALID_PIN;
    if (servo_count >= MAX_SERVOS) return VOLT_ERR_NOT_SUPPORTED;
    if (find_servo(pin) >= 0) return VOLT_ERR_PIN_IN_USE;

    // RP2040: each GPIO maps to a PWM slice (pin/2) and channel (pin%2)
    gpio_set_function(pin, GPIO_FUNC_PWM);
    uint slice = pwm_gpio_to_slice_num(pin);
    uint channel = pwm_gpio_to_channel(pin);

    // 125MHz / 50Hz = 2,500,000. Use clkdiv=40, wrap=62500-1
    // 125MHz / 40 = 3.125MHz. 3,125,000 / 62500 = 50Hz exactly.
    uint16_t wrap_val = 62500 - 1;
    pwm_set_clkdiv(slice, 40.0f);
    pwm_set_wrap(slice, wrap_val);
    pwm_set_enabled(slice, true);

    uint8_t idx = servo_count;
    servos[idx] = (servo_state_t){
        .pin = pin,
        .attached = true,
        .min_us = min_us,
        .max_us = max_us,
        .max_angle = 180.0f,
        .current_angle = 0.0f,
        .slice = slice,
        .channel = channel,
        .wrap_val = wrap_val,
    };
    servo_count++;

    // Move to center
    servo_write(pin, 90.0f);

    return VOLT_OK;
}

volt_error_t servo_detach(uint8_t pin)
{
    int idx = find_servo(pin);
    if (idx < 0) return VOLT_ERR_INVALID_PIN;

    uint slice = servos[idx].slice;
    uint channel = servos[idx].channel;

    // Set channel level to 0 (stop PWM on this channel)
    pwm_set_chan_level(slice, channel, 0);

    // Check if any other servo on this slice is using the other channel
    bool other_channel_in_use = false;
    for (int i = 0; i < MAX_SERVOS; i++) {
        if (i != idx && servos[i].attached &&
            servos[i].slice == slice &&
            servos[i].channel != channel) {
            other_channel_in_use = true;
            break;
        }
    }

    // Only disable the entire slice if no other servo uses the other channel
    if (!other_channel_in_use) {
        pwm_set_enabled(slice, false);
    }

    gpio_deinit(pin);
    servos[idx].attached = false;

    return VOLT_OK;
}

volt_error_t servo_write(uint8_t pin, float angle)
{
    int idx = find_servo(pin);
    if (idx < 0) return VOLT_ERR_INVALID_PIN;

    servo_state_t *s = &servos[idx];
    if (angle < 0.0f) angle = 0.0f;
    if (angle > s->max_angle) angle = s->max_angle;

    uint16_t pulse = servo_angle_to_us(pin, angle);
    set_pulse_us(s, pulse);
    s->current_angle = angle;

    return VOLT_OK;
}

volt_error_t servo_write_us(uint8_t pin, uint16_t microseconds)
{
    int idx = find_servo(pin);
    if (idx < 0) return VOLT_ERR_INVALID_PIN;

    servo_state_t *s = &servos[idx];
    if (microseconds < s->min_us) microseconds = s->min_us;
    if (microseconds > s->max_us) microseconds = s->max_us;

    set_pulse_us(s, microseconds);
    float range_us = (float)(s->max_us - s->min_us);
    s->current_angle = (float)(microseconds - s->min_us) / range_us * s->max_angle;

    return VOLT_OK;
}

float servo_read(uint8_t pin)
{
    int idx = find_servo(pin);
    if (idx < 0) return 0.0f;
    return servos[idx].current_angle;
}

volt_error_t servo_set_max_angle(uint8_t pin, float max_angle)
{
    int idx = find_servo(pin);
    if (idx < 0) return VOLT_ERR_INVALID_PIN;
    if (max_angle <= 0.0f) return VOLT_ERR_INVALID_PIN;
    servos[idx].max_angle = max_angle;
    return VOLT_OK;
}

uint16_t servo_angle_to_us(uint8_t pin, float angle)
{
    int idx = find_servo(pin);
    if (idx < 0) return SERVO_DEFAULT_MIN_US;

    servo_state_t *s = &servos[idx];
    float fraction = angle / s->max_angle;
    if (fraction < 0.0f) fraction = 0.0f;
    if (fraction > 1.0f) fraction = 1.0f;

    return s->min_us + (uint16_t)(fraction * (float)(s->max_us - s->min_us));
}

// ============================================================
// Smooth movement
// ============================================================

#define EASE_STEP_MS 20

volt_error_t servo_ease_to(uint8_t pin, float angle,
                           uint32_t duration_ms, uint8_t easing)
{
    int idx = find_servo(pin);
    if (idx < 0) return VOLT_ERR_INVALID_PIN;

    servo_state_t *s = &servos[idx];
    float start_angle = s->current_angle;

    if (angle < 0.0f) angle = 0.0f;
    if (angle > s->max_angle) angle = s->max_angle;

    if (duration_ms < EASE_STEP_MS) {
        return servo_write(pin, angle);
    }

    uint32_t steps = duration_ms / EASE_STEP_MS;
    for (uint32_t i = 1; i <= steps; i++) {
        float t = (float)i / (float)steps;
        float eased = apply_easing(t, easing);
        float a = start_angle + (angle - start_angle) * eased;
        servo_write(pin, a);
        sleep_ms(EASE_STEP_MS);
    }

    servo_write(pin, angle);
    return VOLT_OK;
}

volt_error_t servo_sweep(uint8_t pin, float from_angle, float to_angle,
                         uint32_t duration_ms)
{
    return servo_sweep_ease(pin, from_angle, to_angle, duration_ms, SERVO_LINEAR);
}

volt_error_t servo_sweep_ease(uint8_t pin, float from_angle, float to_angle,
                              uint32_t duration_ms, uint8_t easing)
{
    servo_write(pin, from_angle);
    sleep_ms(50);

    int idx = find_servo(pin);
    if (idx < 0) return VOLT_ERR_INVALID_PIN;

    servo_state_t *s = &servos[idx];
    if (from_angle < 0.0f) from_angle = 0.0f;
    if (from_angle > s->max_angle) from_angle = s->max_angle;
    if (to_angle < 0.0f) to_angle = 0.0f;
    if (to_angle > s->max_angle) to_angle = s->max_angle;

    uint32_t steps = duration_ms / EASE_STEP_MS;
    if (steps < 1) steps = 1;

    for (uint32_t i = 1; i <= steps; i++) {
        float t = (float)i / (float)steps;
        float eased = apply_easing(t, easing);
        float a = from_angle + (to_angle - from_angle) * eased;
        servo_write(pin, a);
        sleep_ms(EASE_STEP_MS);
    }

    servo_write(pin, to_angle);
    return VOLT_OK;
}

// ============================================================
// Multi-servo synchronized movement
// ============================================================

void servo_sync_begin(void)
{
    sync_count = 0;
    sync_active = true;
}

volt_error_t servo_sync_move(uint8_t pin, float angle,
                             uint32_t duration_ms, uint8_t easing)
{
    if (!sync_active) return VOLT_ERR_INTERNAL;
    if (sync_count >= MAX_SYNC_MOVES) return VOLT_ERR_NOT_SUPPORTED;

    int idx = find_servo(pin);
    if (idx < 0) return VOLT_ERR_INVALID_PIN;

    sync_buffer[sync_count++] = (sync_move_t){
        .servo_idx = (uint8_t)idx,
        .target_angle = angle,
        .duration_ms = duration_ms,
        .easing = easing,
    };

    return VOLT_OK;
}

volt_error_t servo_sync_go(void)
{
    if (!sync_active || sync_count == 0) return VOLT_OK;

    uint32_t max_duration = 0;
    for (uint8_t i = 0; i < sync_count; i++) {
        if (sync_buffer[i].duration_ms > max_duration)
            max_duration = sync_buffer[i].duration_ms;
    }

    float start_angles[MAX_SYNC_MOVES];
    for (uint8_t i = 0; i < sync_count; i++) {
        start_angles[i] = servos[sync_buffer[i].servo_idx].current_angle;
    }

    uint32_t steps = max_duration / EASE_STEP_MS;
    if (steps < 1) steps = 1;

    for (uint32_t step = 1; step <= steps; step++) {
        for (uint8_t i = 0; i < sync_count; i++) {
            sync_move_t *m = &sync_buffer[i];
            servo_state_t *s = &servos[m->servo_idx];

            uint32_t own_steps = m->duration_ms / EASE_STEP_MS;
            if (own_steps < 1) own_steps = 1;

            float t = (step >= own_steps) ? 1.0f : (float)step / (float)own_steps;
            float eased = apply_easing(t, m->easing);
            float angle = start_angles[i] + (m->target_angle - start_angles[i]) * eased;
            servo_write(s->pin, angle);
        }
        sleep_ms(EASE_STEP_MS);
    }

    for (uint8_t i = 0; i < sync_count; i++) {
        servo_write(servos[sync_buffer[i].servo_idx].pin,
                    sync_buffer[i].target_angle);
    }

    sync_active = false;
    sync_count = 0;
    return VOLT_OK;
}
