#ifndef VOLT_H
#define VOLT_H

// Master include for the Volt framework.
// Generated .c files include individual headers, but this provides volt_init().

#include "volt/error.h"
#include "volt/log.h"
#include "volt/pin.h"
#include "volt/task.h"
#include "volt/system.h"

// Initialize the Volt framework. Call once at the start of app_main().
// Currently a no-op — individual modules init themselves when first used.
// Reserved for future global initialization.
static inline void volt_init(void) {
    // GPIO ISR service installed on first pin_on_change() call
    // NVS initialized by storage_init()
    // WiFi initialized by wifi_connect()
}

#endif // VOLT_H
