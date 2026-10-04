# Volt framework sources for a generated project (`volt build`). The
# project's main/CMakeLists.txt includes this file, then registers main.c,
# VOLT_CORE_SRCS and the device drivers the program uses.
set(_VOLT_FW "${CMAKE_CURRENT_LIST_DIR}")

set(VOLT_CORE_SRCS
    "${_VOLT_FW}/src/error.c"
    "${_VOLT_FW}/src/log.c"
    "${_VOLT_FW}/src/pin.c"
    "${_VOLT_FW}/src/task.c"
    "${_VOLT_FW}/src/wifi.c"
    "${_VOLT_FW}/src/mqtt.c"
    "${_VOLT_FW}/src/storage.c"
    "${_VOLT_FW}/src/http.c"
    "${_VOLT_FW}/src/system.c"
    "${_VOLT_FW}/src/i2c.c"
    "${_VOLT_FW}/src/spi.c"
    "${_VOLT_FW}/src/uart.c"
    "${_VOLT_FW}/src/servo.c"
    "${_VOLT_FW}/src/touch.c"
)

set(VOLT_INCLUDE_DIRS "${_VOLT_FW}/../include")
