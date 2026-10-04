#include <stdbool.h>
#include <stdint.h>
#include <math.h>
#include "volt/servo.h"
#include "volt/log.h"
#include "driver/ledc.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

// ============================================================
// Internal state
// ============================================================

#define MAX_SERVOS 16
#define SERVO_FREQ_HZ 50      // Standard servo PWM frequency
#define SERVO_TIMER_BITS 16   // 16-bit timer for fine pulse control
// At 50Hz, period = 20000us. With 16-bit timer (65535 counts):
// 1 count = 20000/65535 = ~0.305us resolution

typedef struct {
    uint8_t pin;
    bool attached;
    uint16_t min_us;
    uint16_t max_us;
    float max_angle;
    float current_angle;
    int8_t ledc_channel;
} servo_state_t;

static servo_state_t servos[MAX_SERVOS];
static uint8_t servo_count = 0;
static bool timer_configured = false;

// Sync buffer for multi-servo moves
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
// Easing functions — all take t in [0,1], return value in [0,1]
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
    // Overshoots by ~10%, then settles
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

static uint32_t us_to_duty(uint16_t pulse_us)
{
    // Period at 50Hz = 20000us. 16-bit timer = 65535 counts.
    // duty = pulse_us / 20000 * 65535
    return (uint32_t)((float)pulse_us / 20000.0f * 65535.0f);
}

static void set_pulse_us(servo_state_t *s, uint16_t pulse_us)
{
    uint32_t duty = us_to_duty(pulse_us);
    ledc_set_duty(LEDC_LOW_SPEED_MODE, s->ledc_channel, duty);
    ledc_update_duty(LEDC_LOW_SPEED_MODE, s->ledc_channel);
}

static void configure_timer_once(void)
{
    if (timer_configured) return;

    ledc_timer_config_t timer_cfg = {
        .speed_mode = LEDC_LOW_SPEED_MODE,
        .duty_resolution = SERVO_TIMER_BITS,
        .timer_num = LEDC_TIMER_1,  // Use timer 1 (timer 0 used by PWM pins)
        .freq_hz = SERVO_FREQ_HZ,
        .clk_cfg = LEDC_AUTO_CLK,
    };
    ledc_timer_config(&timer_cfg);
    timer_configured = true;
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
    if (servo_count >= MAX_SERVOS) {
        log_error("servo", "Maximum %d servos reached", MAX_SERVOS);
        return VOLT_ERR_NOT_SUPPORTED;
    }

    // Check if already attached
    if (find_servo(pin) >= 0) {
        log_error("servo", "GPIO %d already has a servo attached", pin);
        return VOLT_ERR_PIN_IN_USE;
    }

    configure_timer_once();

    if (servo_count >= 8) {
        log_error("servo", "LEDC has only 8 hardware channels — servo %d would exceed limit", servo_count);
        return VOLT_ERR_NOT_SUPPORTED;
    }

    uint8_t idx = servo_count;
    uint8_t ch = idx;  // 1:1 channel mapping (LEDC has 8 channels)

    ledc_channel_config_t ch_cfg = {
        .speed_mode = LEDC_LOW_SPEED_MODE,
        .channel = ch,
        .timer_sel = LEDC_TIMER_1,
        .gpio_num = pin,
        .duty = 0,
        .hpoint = 0,
    };
    ledc_channel_config(&ch_cfg);

    servos[idx] = (servo_state_t){
        .pin = pin,
        .attached = true,
        .min_us = min_us,
        .max_us = max_us,
        .max_angle = 180.0f,
        .current_angle = 0.0f,
        .ledc_channel = ch,
    };
    servo_count++;

    // Move to center (90 degrees)
    servo_write(pin, 90.0f);

    log_debug("servo", "GPIO %d attached (LEDC ch%d, %d-%dus)", pin, ch, min_us, max_us);
    return VOLT_OK;
}

volt_error_t servo_detach(uint8_t pin)
{
    int idx = find_servo(pin);
    if (idx < 0) return VOLT_ERR_INVALID_PIN;

    ledc_stop(LEDC_LOW_SPEED_MODE, servos[idx].ledc_channel, 0);
    servos[idx].attached = false;

    log_debug("servo", "GPIO %d detached", pin);
    return VOLT_OK;
}

volt_error_t servo_write(uint8_t pin, float angle)
{
    int idx = find_servo(pin);
    if (idx < 0) return VOLT_ERR_INVALID_PIN;

    servo_state_t *s = &servos[idx];

    // Clamp angle
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

    // Update tracked angle
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

// Step interval for smooth movement (ms). 20ms = 50 updates/sec
// matches the 50Hz servo refresh rate.
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
        // Too short for interpolation — just jump
        return servo_write(pin, angle);
    }

    uint32_t steps = duration_ms / EASE_STEP_MS;
    for (uint32_t i = 1; i <= steps; i++) {
        float t = (float)i / (float)steps;
        float eased = apply_easing(t, easing);
        float a = start_angle + (angle - start_angle) * eased;
        servo_write(pin, a);
        vTaskDelay(EASE_STEP_MS / portTICK_PERIOD_MS);
    }

    // Ensure we land exactly on target
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
    // Move to start position first
    servo_write(pin, from_angle);
    vTaskDelay(50 / portTICK_PERIOD_MS);

    // Ease from start to end
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
        vTaskDelay(EASE_STEP_MS / portTICK_PERIOD_MS);
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

    // Find the longest duration — all moves run for this duration
    uint32_t max_duration = 0;
    for (uint8_t i = 0; i < sync_count; i++) {
        if (sync_buffer[i].duration_ms > max_duration)
            max_duration = sync_buffer[i].duration_ms;
    }

    // Snapshot starting angles
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

            // Each servo has its own duration within the total
            uint32_t own_steps = m->duration_ms / EASE_STEP_MS;
            if (own_steps < 1) own_steps = 1;

            float t;
            if (step >= own_steps) {
                t = 1.0f;  // This servo is done
            } else {
                t = (float)step / (float)own_steps;
            }

            float eased = apply_easing(t, m->easing);
            float angle = start_angles[i] + (m->target_angle - start_angles[i]) * eased;
            servo_write(s->pin, angle);
        }
        vTaskDelay(EASE_STEP_MS / portTICK_PERIOD_MS);
    }

    // Ensure all servos land on target
    for (uint8_t i = 0; i < sync_count; i++) {
        servo_write(servos[sync_buffer[i].servo_idx].pin,
                    sync_buffer[i].target_angle);
    }

    sync_active = false;
    sync_count = 0;
    return VOLT_OK;
}
