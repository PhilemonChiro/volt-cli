#include <stdbool.h>
#include <stdint.h>
#include "volt/task.h"
#include "volt/log.h"

#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_system.h"
#include "esp_attr.h"
#include "esp_timer.h"
#include "esp_rom_sys.h"

// ESP32 has gptimer for hardware timer ISRs; ESP8266 uses esp_timer only
#if !defined(CONFIG_IDF_TARGET_ESP8266) && !defined(ESP8266_RTOS_SDK)
#include "esp_task_wdt.h"
#include "driver/gptimer.h"
#define HAS_GPTIMER 1
#else
#define HAS_GPTIMER 0
#endif

// ============================================================
// Tier 3: Background scheduler (protothreads)
// ============================================================

// How often to log stack/heap health (every 60 seconds)
#define HEALTH_CHECK_INTERVAL_MS 60000

void volt_run(volt_task_entry_t *tasks, int count)
{
    log_info("task", "scheduler started with %d background tasks", count);

#if HAS_GPTIMER
    // Register this task with the watchdog timer.
    esp_task_wdt_add(NULL);
#endif

    uint32_t last_health_check = 0;

    while (1) {
        bool any_ran = false;
        uint32_t now = volt_tick_ms();

        for (int i = 0; i < count; i++) {
            volt_task_t *t = tasks[i].task;

            // Skip finished tasks
            if (t->status == VOLT_TASK_DONE) continue;

            // Skip sleeping tasks that haven't reached their wake time.
            // Uses signed comparison for uint32 wraparound safety (~49 days).
            if (t->status == VOLT_TASK_WAITING && t->wake_at > 0) {
                if ((int32_t)(now - t->wake_at) < 0) continue;
            }

            // Run the task — it will execute until the next yield/sleep/wait
            t->status = VOLT_TASK_RUNNING;
            tasks[i].func(t);
            any_ran = true;
        }

        // Feed the watchdog — proves the scheduler is alive.
        // If a task blocks without yielding, this line is never reached,
        // and the watchdog resets the device.
#if HAS_GPTIMER
        esp_task_wdt_reset();
#endif

        // Periodic health check: stack remaining + heap free
        if (now - last_health_check > HEALTH_CHECK_INTERVAL_MS) {
            last_health_check = now;
            UBaseType_t stack_remaining = uxTaskGetStackHighWaterMark(NULL);
            uint32_t heap_free = esp_get_free_heap_size();
            log_debug("health", "stack_remaining=%lu bytes heap_free=%lu bytes",
                      (unsigned long)(stack_remaining * sizeof(StackType_t)),
                      (unsigned long)heap_free);

            // Warn if stack is getting low (< 512 bytes remaining)
            if (stack_remaining * sizeof(StackType_t) < 512) {
                log_warn("health", "stack low: only %lu bytes remaining!",
                         (unsigned long)(stack_remaining * sizeof(StackType_t)));
            }
        }

        // Always yield at least 1 tick per loop iteration.
        // This ensures the IDLE task runs (feeds its watchdog)
        // and WiFi/BT system tasks get CPU time.
        vTaskDelay(1);
    }
}

// ============================================================
// Tier 2: Priority tasks (FreeRTOS wrapper)
// ============================================================

volt_error_t volt_rt_create(const char *name, volt_rt_func_t func,
                            uint32_t stack_size, void *arg,
                            uint8_t priority, int core)
{
    if (func == NULL || name == NULL) return VOLT_ERR_INVALID_ARG;

    BaseType_t result;

    (void)core;  // ignored on single-core chips
    result = xTaskCreate(func, name, stack_size, arg, priority, NULL);

    if (result != pdPASS) {
        log_error("task", "failed to create priority task '%s' (not enough memory?)", name);
        return VOLT_ERR_TASK_CREATE_FAILED;
    }

    log_info("task", "priority task '%s' created (stack=%lu, priority=%d, core=%d)",
             name, (unsigned long)stack_size, priority, core);
    return VOLT_OK;
}

// ============================================================
// Tier 1: Critical tasks (hardware timer ISR)
// ============================================================

#if HAS_GPTIMER

// Context for ISR: stores both the user function and user arg
typedef struct {
    volt_crit_func_t func;
    void *arg;
} critical_isr_ctx_t;

// Up to 4 critical tasks (ESP32 has 4 hardware timers)
#define MAX_CRITICAL_TASKS 4
static critical_isr_ctx_t s_critical_ctx[MAX_CRITICAL_TASKS];
static int s_critical_count = 0;

// GPTimer ISR callback — called by hardware at the configured rate.
static bool IRAM_ATTR critical_timer_isr(gptimer_handle_t timer,
                                          const gptimer_alarm_event_data_t *event,
                                          void *arg)
{
    critical_isr_ctx_t *ctx = (critical_isr_ctx_t *)arg;
    ctx->func(ctx->arg);
    return false;
}

volt_error_t volt_critical_create(const char *name, volt_crit_func_t func,
                                  uint32_t period_us, void *arg)
{
    if (func == NULL || name == NULL) return VOLT_ERR_INVALID_ARG;
    if (period_us == 0) return VOLT_ERR_INVALID_ARG;
    if (s_critical_count >= MAX_CRITICAL_TASKS) return VOLT_ERR_TOO_MANY_TASKS;

    // Create a hardware timer
    gptimer_handle_t timer = NULL;
    gptimer_config_t timer_cfg = {
        .clk_src = GPTIMER_CLK_SRC_DEFAULT,
        .direction = GPTIMER_COUNT_UP,
        // 1 MHz resolution = 1 tick per microsecond
        .resolution_hz = 1000000,
    };

    esp_err_t err = gptimer_new_timer(&timer_cfg, &timer);
    if (err != ESP_OK) {
        log_error("task", "failed to create timer for '%s' (esp_err=%d)", name, err);
        return VOLT_ERR_TIMER_FAILED;
    }

    // Store user function + arg in context struct (passed to ISR)
    s_critical_ctx[s_critical_count].func = func;
    s_critical_ctx[s_critical_count].arg = arg;

    // Set up alarm to fire every period_us microseconds
    gptimer_alarm_config_t alarm_cfg = {
        .alarm_count = period_us,
        .reload_count = 0,
        .flags.auto_reload_on_alarm = true,
    };

    // Register ISR — pass our context struct, not just the function pointer
    gptimer_event_callbacks_t cbs = {
        .on_alarm = critical_timer_isr,
    };

    // Use a local index — only increment count after ALL steps succeed
    int idx = s_critical_count;

    err = gptimer_register_event_callbacks(timer, &cbs, &s_critical_ctx[idx]);
    if (err != ESP_OK) {
        log_error("task", "failed to register ISR for '%s'", name);
        gptimer_del_timer(timer);
        return VOLT_ERR_TIMER_FAILED;
    }

    err = gptimer_set_alarm_action(timer, &alarm_cfg);
    if (err != ESP_OK) {
        log_error("task", "failed to set alarm for '%s'", name);
        gptimer_del_timer(timer);
        return VOLT_ERR_TIMER_FAILED;
    }

    // Enable and start the timer
    gptimer_enable(timer);
    gptimer_start(timer);

    // All steps succeeded — now commit the slot
    s_critical_count++;

    log_info("task", "critical task '%s' created (period=%luus, rate=%luHz)",
             name, (unsigned long)period_us,
             (unsigned long)(1000000 / period_us));
    return VOLT_OK;
}

#else  // !HAS_GPTIMER — use esp_timer for critical tasks

volt_error_t volt_critical_create(const char *name, volt_crit_func_t func,
                                  uint32_t period_us, void *arg)
{
    if (func == NULL || name == NULL) return VOLT_ERR_INVALID_ARG;

    esp_timer_create_args_t timer_args = {
        .callback = (esp_timer_cb_t)func,
        .arg = arg,
        .name = name,
    };
    esp_timer_handle_t timer;
    esp_err_t err = esp_timer_create(&timer_args, &timer);
    if (err != ESP_OK) return VOLT_ERR_TIMER_FAILED;

    err = esp_timer_start_periodic(timer, period_us);
    if (err != ESP_OK) return VOLT_ERR_TIMER_FAILED;

    log_info("task", "critical task '%s' created via esp_timer (period=%luus)",
             name, (unsigned long)period_us);
    return VOLT_OK;
}

#endif  // HAS_GPTIMER

// ============================================================
// Shared: tick counters
// ============================================================

void volt_delay_ms(uint32_t ms)
{
    // Whole ticks yield; the sub-tick remainder busy-waits. At 100 Hz a plain
    // vTaskDelay(ms / 10) turns 1-9 ms delays into 0 (broke LCD clear timing).
    vTaskDelay(ms / portTICK_PERIOD_MS);
    uint32_t rem = ms % portTICK_PERIOD_MS;
    if (rem) esp_rom_delay_us(rem * 1000);
}

uint32_t volt_tick_ms(void)
{
    return xTaskGetTickCount() * portTICK_PERIOD_MS;
}

uint64_t volt_tick_us(void)
{
    // esp_timer provides 64-bit microsecond counter
    // Works from any context including ISR
    return (uint64_t)esp_timer_get_time();
}
