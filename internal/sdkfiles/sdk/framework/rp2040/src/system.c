#include "volt/system.h"
#include "pico/stdlib.h"
#include "pico/time.h"
#include "pico/unique_id.h"
#include "hardware/watchdog.h"

void system_restart(void)
{
    watchdog_reboot(0, 0, 0);
    // If watchdog reboot not available, spin forever
    while (1) tight_loop_contents();
}

uint32_t system_free_heap(void)
{
    // RP2040 doesn't have a standard heap allocator.
    // This returns 0 to indicate heap tracking is not available.
    // For stack remaining, use system_stack_remaining() instead.
    // Applications should not rely on this for memory management.
    return 0;
}

uint32_t system_uptime(void)
{
    return to_ms_since_boot(get_absolute_time()) / 1000;
}

uint32_t system_micros(void)
{
    return time_us_32();
}

uint32_t system_min_free_heap(void)
{
    return 0; // heap not tracked, see system_free_heap
}

uint32_t system_id(void)
{
    pico_unique_board_id_t id;
    pico_get_unique_board_id(&id);
    // Return first 4 bytes as a uint32
    return (uint32_t)id.id[0] | ((uint32_t)id.id[1] << 8) |
           ((uint32_t)id.id[2] << 16) | ((uint32_t)id.id[3] << 24);
}

void system_deep_sleep(uint64_t duration_us)
{
    // RP2040 doesn't have true deep sleep like ESP32
    // Use dormant mode or just sleep
    sleep_us(duration_us);
}

void system_light_sleep(void)
{
    // Light sleep — use WFI (Wait For Interrupt)
    __wfi();
}

// ---- Watchdog ----

static bool wdt_enabled = false;

// Pico SDK has its own watchdog_enable() with different signature.
// We use volt_ prefix internally, aliased via the header.
void volt_wdt_enable(uint32_t timeout_ms)
{
    if (wdt_enabled) return;
    // RP2040 watchdog max is ~8.3 seconds (8388ms)
    if (timeout_ms > 8388) timeout_ms = 8388;
    watchdog_enable(timeout_ms, true);  // Pico SDK call, true = pause on debug
    wdt_enabled = true;
}

void volt_wdt_feed(void)
{
    if (wdt_enabled) {
        watchdog_update();
    }
}

void volt_wdt_disable(void)
{
    // RP2040 watchdog cannot be disabled once enabled — only reset stops it.
    // This is a hardware limitation documented by Pico SDK.
    // The function exists for API compatibility but has no effect.
    // Applications must either:
    // 1. Never enable watchdog if they need to stop it
    // 2. Use a long timeout and let it reset if needed
    // 3. Use software-based timeout tracking instead
    wdt_enabled = false;
}
