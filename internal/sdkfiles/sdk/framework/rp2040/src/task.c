#include <stdbool.h>
#include <stdint.h>
#include "volt/task.h"
#include "volt/log.h"
#include "pico/stdlib.h"
#include "pico/time.h"
#include "hardware/timer.h"

// RP2040 has no RTOS by default — bare-metal cooperative scheduler

void volt_run(volt_task_entry_t *tasks, int count)
{
    log_info("task", "scheduler started with %d background tasks", count);

    while (1) {
        bool any_ran = false;
        uint32_t now = volt_tick_ms();

        for (int i = 0; i < count; i++) {
            volt_task_t *t = tasks[i].task;
            if (t->status == VOLT_TASK_DONE) continue;
            if (t->status == VOLT_TASK_WAITING && t->wake_at > 0) {
                if ((int32_t)(now - t->wake_at) < 0) continue;
            }
            t->status = VOLT_TASK_RUNNING;
            tasks[i].func(t);
            any_ran = true;
        }

        // Only sleep when no task ran — keeps timing precise
        // Sleep 1ms to avoid 100% CPU burn on battery-powered devices.
        // tight_loop_contents() yields to USB stack but burns CPU.
        if (!any_ran) {
            sleep_ms(1);
        }
    }
}

// RP2040 doesn't have FreeRTOS by default — stub for priority tasks
// Future: integrate FreeRTOS-Kernel for RP2040 when needed
volt_error_t volt_rt_create(const char *name, volt_rt_func_t func,
                            uint32_t stack_size, void *arg,
                            uint8_t priority, int core)
{
    (void)stack_size; (void)priority; (void)core;
    log_warn("task", "priority tasks not yet supported on RP2040 (no RTOS). '%s' will not run.", name);
    return VOLT_ERR_NOT_SUPPORTED;
}

// RP2040 hardware timer for critical tasks
// RP2040 has 4 hardware timer alarms
#define MAX_CRITICAL_TASKS 4

typedef struct {
    volt_crit_func_t func;
    void *arg;
    uint32_t period_us;
    alarm_id_t alarm_id;
} critical_ctx_t;

static critical_ctx_t s_critical[MAX_CRITICAL_TASKS];
static int s_critical_count = 0;

static int64_t critical_alarm_callback(alarm_id_t id, void *arg)
{
    critical_ctx_t *ctx = (critical_ctx_t *)arg;
    ctx->func(ctx->arg);
    // Return negative period to reschedule (pico SDK convention)
    return -(int64_t)ctx->period_us;
}

volt_error_t volt_critical_create(const char *name, volt_crit_func_t func,
                                  uint32_t period_us, void *arg)
{
    if (func == NULL || name == NULL) return VOLT_ERR_INVALID_ARG;
    if (period_us == 0) return VOLT_ERR_INVALID_ARG;
    if (s_critical_count >= MAX_CRITICAL_TASKS) return VOLT_ERR_TOO_MANY_TASKS;

    int idx = s_critical_count;
    s_critical[idx].func = func;
    s_critical[idx].arg = arg;
    s_critical[idx].period_us = period_us;

    s_critical[idx].alarm_id = add_alarm_in_us(period_us, critical_alarm_callback,
                                                &s_critical[idx], true);
    if (s_critical[idx].alarm_id < 0) {
        log_error("task", "failed to create alarm for '%s'", name);
        return VOLT_ERR_TIMER_FAILED;
    }

    s_critical_count++;
    log_info("task", "critical task '%s' created (period=%luus)", name, (unsigned long)period_us);
    return VOLT_OK;
}

void volt_delay_ms(uint32_t ms)
{
    sleep_ms(ms);
}

uint32_t volt_tick_ms(void)
{
    return to_ms_since_boot(get_absolute_time());
}

uint64_t volt_tick_us(void)
{
    return to_us_since_boot(get_absolute_time());
}
