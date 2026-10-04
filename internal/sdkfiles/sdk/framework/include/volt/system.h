#ifndef VOLT_SYSTEM_H
#define VOLT_SYSTEM_H

#include <stdint.h>

// Reboot the device immediately.
void system_restart(void);

// Available heap RAM in bytes.
uint32_t system_free_heap(void);

// Seconds since last boot.
uint32_t system_uptime(void);

// Microseconds since boot (wraps after ~71 min, like Arduino micros()).
uint32_t system_micros(void);

// Lowest free heap seen since boot, in bytes (0 where untracked).
uint32_t system_min_free_heap(void);

// Chip/device unique ID.
uint32_t system_id(void);

// Deep sleep — all RAM lost, wakes via timer.
// duration_us: microseconds to sleep.
void system_deep_sleep(uint64_t duration_us);

// Light sleep — RAM preserved, faster wake.
void system_light_sleep(void);

// ---- Watchdog Timer ----
// Reboots the device if the program hangs or crashes.
// Call watchdog_feed() periodically to prove the code is still running.
// If feed() is not called within the timeout, the chip resets.

// Enable watchdog with timeout in milliseconds. Typical: 5000-30000ms.
void volt_wdt_enable(uint32_t timeout_ms);

// Feed (kick) the watchdog — resets the countdown timer.
// Must be called regularly to prevent a watchdog reset.
void volt_wdt_feed(void);

// Disable the watchdog timer.
void volt_wdt_disable(void);

#endif // VOLT_SYSTEM_H
