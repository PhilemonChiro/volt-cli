#include "volt/system.h"
#include "volt/log.h"
#include "esp_system.h"
#include "esp_timer.h"
#include "esp_sleep.h"
#include "esp_task_wdt.h"

void system_restart(void)
{
    esp_restart();
}

uint32_t system_free_heap(void)
{
    return esp_get_free_heap_size();
}

uint32_t system_uptime(void)
{
    return (uint32_t)(esp_timer_get_time() / 1000000);
}

uint32_t system_micros(void)
{
    return (uint32_t)esp_timer_get_time();
}

uint32_t system_min_free_heap(void)
{
    return esp_get_minimum_free_heap_size();
}

uint32_t system_id(void)
{
    return 0; // placeholder
}

void system_deep_sleep(uint64_t duration_us)
{
    esp_deep_sleep(duration_us);
}

void system_light_sleep(void)
{
    esp_light_sleep_start();
}

// ---- Watchdog ----

static bool wdt_enabled = false;

void volt_wdt_enable(uint32_t timeout_ms)
{
    if (wdt_enabled) return;

    esp_task_wdt_config_t cfg = {
        .timeout_ms = timeout_ms,
        .idle_core_mask = 0,
        .trigger_panic = true,
    };
    esp_task_wdt_reconfigure(&cfg);
    esp_task_wdt_add(NULL);
    wdt_enabled = true;
    log_info("system", "watchdog enabled (%lums timeout)", (unsigned long)timeout_ms);
}

void volt_wdt_feed(void)
{
    if (wdt_enabled) {
        esp_task_wdt_reset();
    }
}

void volt_wdt_disable(void)
{
    if (!wdt_enabled) return;
    esp_task_wdt_delete(NULL);
    wdt_enabled = false;
    log_info("system", "watchdog disabled");
}
