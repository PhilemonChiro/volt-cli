#include "volt/log.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

// ANSI color codes for terminal output
static const char *level_colors[] = {
    "",           // NONE
    "\033[31m",   // ERROR — red
    "\033[33m",   // WARN  — yellow
    "\033[32m",   // INFO  — green
    "\033[36m",   // DEBUG — cyan
};

static const char *level_names[] = {
    "",
    "ERROR",
    "WARN ",
    "INFO ",
    "DEBUG",
};

// Reset color back to default after each line
#define COLOR_RESET "\033[0m"

void volt_log_write(int level, const char *tag, const char *fmt, ...)
{
    // Clamp level to valid range to prevent array OOB access
    if (level < 0) level = 0;
    if (level > 4) level = 4;

    // Get milliseconds since boot from FreeRTOS tick counter
    uint32_t ms = xTaskGetTickCount() * portTICK_PERIOD_MS;
    uint32_t h  = (ms / 3600000) % 24;
    uint32_t m  = (ms / 60000) % 60;
    uint32_t s  = (ms / 1000) % 60;
    uint32_t ml = ms % 1000;

    // Print: color + timestamp + level + tag + message + reset
    printf("%s%02lu:%02lu:%02lu.%03lu [%s] %s: ",
           level_colors[level],
           (unsigned long)h, (unsigned long)m,
           (unsigned long)s, (unsigned long)ml,
           level_names[level], tag);

    // Print the user's formatted message
    va_list args;
    va_start(args, fmt);
    vprintf(fmt, args);
    va_end(args);

    // End line and reset color
    printf("%s\n", COLOR_RESET);
}
