#ifndef VOLT_TASK_H
#define VOLT_TASK_H

#include <stdint.h>
#include <stdbool.h>
#include "volt/error.h"

// ============================================================
// Tier 3: Background Tasks (Protothreads)
//
// Lightweight cooperative tasks. 12 bytes each. No stack.
// For: LED blinks, logging, telemetry, display updates.
// The transpiler generates these for any task without "strict"
// that doesn't use blocking APIs (WiFi, MQTT).
// ============================================================

// Task status
#define VOLT_TASK_RUNNING  0
#define VOLT_TASK_WAITING  1
#define VOLT_TASK_DONE     2

// Task state — one per background task, 12 bytes
typedef struct {
    uint16_t _line;       // Duff's device resumption point
    uint8_t  status;      // RUNNING, WAITING, or DONE
    uint32_t wake_at;     // tick count to wake at (for SLEEP_MS)
    const char *name;     // for debug output ("blink", "logger")
} volt_task_t;

// Task function signature
typedef void (*volt_task_func_t)(volt_task_t *t);

// Pairs a function with its state struct
typedef struct {
    volt_task_func_t func;
    volt_task_t *task;
} volt_task_entry_t;

// --- Protothread macros ---

// Place at the start of every background task function.
// Opens the Duff's device switch — jumps to the saved resumption point.
#define VOLT_TASK_BEGIN(t)  switch((t)->_line) { case 0:

// Place at the end of every background task function.
// Marks the task as done.
#define VOLT_TASK_END(t)    } (t)->status = VOLT_TASK_DONE;

// Suspend this task for a number of milliseconds.
// Uses absolute timing to prevent drift. Uses signed comparison
// for wraparound safety (uint32 wraps at ~49 days, signed diff handles it).
#define VOLT_TASK_SLEEP_MS(t, ms) \
    do { \
        if ((t)->wake_at == 0) { \
            (t)->wake_at = volt_tick_ms() + (ms); \
        } else { \
            (t)->wake_at += (ms); \
        } \
        (t)->_line = __LINE__; (t)->status = VOLT_TASK_WAITING; return; \
        case __LINE__: \
        if ((int32_t)(volt_tick_ms() - (t)->wake_at) < 0) { \
            (t)->status = VOLT_TASK_WAITING; return; \
        } \
    } while(0)

// Yield to other tasks and resume on the next scheduler cycle.
// Use when you want to be cooperative without sleeping.
#define VOLT_TASK_YIELD(t) \
    do { \
        (t)->_line = __LINE__; (t)->status = VOLT_TASK_WAITING; return; \
        case __LINE__:; \
    } while(0)

// ---- Counter-based yield macros (used by generated code) ----
// These use explicit label numbers instead of __LINE__, making them
// safe to use with #line directives (which change __LINE__).
// The transpiler assigns sequential label numbers to each yield point.

#define VOLT_YIELD_LABEL(t, label, ms) \
    do { \
        if ((t)->wake_at == 0) { \
            (t)->wake_at = volt_tick_ms() + (ms); \
        } else { \
            (t)->wake_at += (ms); \
        } \
        (t)->_line = label; (t)->status = VOLT_TASK_WAITING; return; \
        case label: \
        if ((int32_t)(volt_tick_ms() - (t)->wake_at) < 0) { \
            (t)->status = VOLT_TASK_WAITING; return; \
        } \
    } while(0)

#define VOLT_WAIT_LABEL(t, label, cond) \
    do { \
        (t)->_line = label; \
        case label: \
        if (!(cond)) { (t)->status = VOLT_TASK_WAITING; return; } \
    } while(0)

// ---- __LINE__-based macros (for hand-written C code) ----

// Suspend until a condition becomes true.
// Checked every scheduler cycle (no busy-wait — other tasks run between checks).
#define VOLT_TASK_WAIT_UNTIL(t, cond) \
    do { \
        (t)->_line = __LINE__; \
        case __LINE__: \
        if (!(cond)) { (t)->status = VOLT_TASK_WAITING; return; } \
    } while(0)

// Run all background tasks cooperatively. Blocks forever.
// Yields to FreeRTOS (1ms) when all tasks are waiting,
// allowing WiFi/BT system tasks to run.
void volt_run(volt_task_entry_t *tasks, int count);

// ============================================================
// Tier 2: Priority Tasks (FreeRTOS wrapper)
//
// Real preemptive tasks with their own stack. Higher priority
// tasks preempt lower ones. For: WiFi handling, MQTT, sensor
// fusion, protocol stacks — anything needing a stack or callbacks.
// The transpiler generates these for tasks using blocking APIs.
// ============================================================

#define VOLT_PRIORITY_LOW       2
#define VOLT_PRIORITY_NORMAL    5
#define VOLT_PRIORITY_HIGH     10
#define VOLT_PRIORITY_CRITICAL 20

// Priority task function signature (same as FreeRTOS)
typedef void (*volt_rt_func_t)(void *arg);

// Create a preemptive task with its own stack.
// - name: for debugging ("wifi_handler")
// - func: the function to run (must loop forever or delete itself)
// - stack_size: bytes of stack (minimum 2048, recommend 4096)
// - arg: passed to func as parameter
// - priority: VOLT_PRIORITY_* (higher number = more urgent)
// - core: 0 (PRO_CPU), 1 (APP_CPU), or -1 (any core)
volt_error_t volt_rt_create(const char *name, volt_rt_func_t func,
                            uint32_t stack_size, void *arg,
                            uint8_t priority, int core);

// ============================================================
// Tier 1: Critical Tasks (Hardware Timer)
//
// Runs inside a hardware timer interrupt at a guaranteed rate.
// NOTHING can delay these — they preempt everything.
// For: PID loops, motor control, sensor sampling at fixed rates.
// The transpiler generates these for tasks with "strict" timing.
//
// RULES (enforced by transpiler, must be followed in raw C):
//   - Must complete in microseconds, not milliseconds
//   - No log(), printf(), malloc(), WiFi, MQTT, or any blocking call
//   - No VOLT_TASK_SLEEP — runs once per period, not a loop
//   - Only direct register reads/writes and math
// ============================================================

// Critical task function signature — called from ISR context
typedef void (*volt_crit_func_t)(void *arg);

// Create a hardware-timer-driven task.
// - name: for debugging ("motor_pid")
// - func: called every period_us microseconds from ISR
// - period_us: period in microseconds (e.g., 1000 = 1ms = 1kHz)
// - arg: passed to func
volt_error_t volt_critical_create(const char *name, volt_crit_func_t func,
                                  uint32_t period_us, void *arg);

// ============================================================
// Shared utilities
// ============================================================

// Blocking delay for priority tasks. Yields to OS/other tasks.
void volt_delay_ms(uint32_t ms);

// Millisecond counter since boot. Works from any context.
uint32_t volt_tick_ms(void);

// Microsecond counter since boot. For critical task timing.
// 64-bit to avoid 32-bit overflow (32-bit overflows after ~71 minutes).
uint64_t volt_tick_us(void);

#endif // VOLT_TASK_H
