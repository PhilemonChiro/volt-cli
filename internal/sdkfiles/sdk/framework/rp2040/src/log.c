#include "volt/log.h"
#include "pico/stdlib.h"
#include "pico/time.h"

static const char *level_names[] = {
    "",
    "ERROR",
    "WARN ",
    "INFO ",
    "DEBUG",
};

void volt_log_write(int level, const char *tag, const char *fmt, ...)
{
    // Clamp level to valid range to prevent array OOB access
    if (level < 0) level = 0;
    if (level > 4) level = 4;

    uint32_t ms = to_ms_since_boot(get_absolute_time());
    uint32_t h  = (ms / 3600000) % 24;
    uint32_t m  = (ms / 60000) % 60;
    uint32_t s  = (ms / 1000) % 60;
    uint32_t ml = ms % 1000;

    printf("%02lu:%02lu:%02lu.%03lu [%s] %s: ",
           (unsigned long)h, (unsigned long)m,
           (unsigned long)s, (unsigned long)ml,
           level_names[level], tag);

    va_list args;
    va_start(args, fmt);
    vprintf(fmt, args);
    va_end(args);

    printf("\n");
}
