#ifndef VOLT_LOG_H
#define VOLT_LOG_H

#include <stdio.h>
#include <stdarg.h>

// Log levels — higher number = more verbose
#define VOLT_LOG_NONE  0
#define VOLT_LOG_ERROR 1
#define VOLT_LOG_WARN  2
#define VOLT_LOG_INFO  3
#define VOLT_LOG_DEBUG 4

// Default: show everything. Override at build time with -DVOLT_LOG_LEVEL=2
#ifndef VOLT_LOG_LEVEL
#define VOLT_LOG_LEVEL VOLT_LOG_DEBUG
#endif

// Public logging macros — these are what you call in code.
// The if() check is compile-time when VOLT_LOG_LEVEL is a constant,
// so disabled levels produce zero code (compiler strips them).
#define log_error(tag, fmt, ...) \
    do { if (VOLT_LOG_LEVEL >= VOLT_LOG_ERROR) \
        volt_log_write(VOLT_LOG_ERROR, tag, fmt, ##__VA_ARGS__); } while(0)

#define log_warn(tag, fmt, ...) \
    do { if (VOLT_LOG_LEVEL >= VOLT_LOG_WARN) \
        volt_log_write(VOLT_LOG_WARN, tag, fmt, ##__VA_ARGS__); } while(0)

#define log_info(tag, fmt, ...) \
    do { if (VOLT_LOG_LEVEL >= VOLT_LOG_INFO) \
        volt_log_write(VOLT_LOG_INFO, tag, fmt, ##__VA_ARGS__); } while(0)

#define log_debug(tag, fmt, ...) \
    do { if (VOLT_LOG_LEVEL >= VOLT_LOG_DEBUG) \
        volt_log_write(VOLT_LOG_DEBUG, tag, fmt, ##__VA_ARGS__); } while(0)

// Internal — don't call directly, use the macros above
void volt_log_write(int level, const char *tag, const char *fmt, ...);

#endif // VOLT_LOG_H
