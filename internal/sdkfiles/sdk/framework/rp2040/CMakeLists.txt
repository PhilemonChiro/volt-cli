cmake_minimum_required(VERSION 3.13)

# Pull in pico SDK (path set by PICO_SDK_PATH env var)
include($ENV{PICO_SDK_PATH}/pico_sdk_init.cmake)

project(volt-rp2040 C CXX ASM)
set(CMAKE_C_STANDARD 11)

pico_sdk_init()

# Discover device drivers, then compile ONLY the ones this program uses (see the
# esp32 CMakeLists for the rationale): a driver whose <prefix>_ never appears in
# main.c isn't needed and must not be compiled, so one unused/broken installed
# driver can't break an unrelated build.
file(GLOB ALL_DEVICE_DRIVERS "${CMAKE_CURRENT_SOURCE_DIR}/../../devices/*/driver.c")
set(DEVICE_DRIVERS "")
set(_MAIN_C "${CMAKE_CURRENT_SOURCE_DIR}/main/main.c")
if(EXISTS "${_MAIN_C}")
    set_property(DIRECTORY APPEND PROPERTY CMAKE_CONFIGURE_DEPENDS "${_MAIN_C}")
    file(READ "${_MAIN_C}" _MAIN_C_TEXT)
    foreach(_drv ${ALL_DEVICE_DRIVERS})
        get_filename_component(_dir "${_drv}" DIRECTORY)
        get_filename_component(_prefix "${_dir}" NAME)
        string(FIND "${_MAIN_C_TEXT}" "${_prefix}_" _idx)
        if(NOT _idx EQUAL -1)
            list(APPEND DEVICE_DRIVERS "${_drv}")
        endif()
    endforeach()
else()
    set(DEVICE_DRIVERS ${ALL_DEVICE_DRIVERS})
endif()

add_executable(volt-rp2040
    main/main.c
    src/pin.c
    src/task.c
    src/log.c
    src/error.c
    src/system.c
    src/storage.c
    src/i2c.c
    src/spi.c
    src/uart.c
    src/servo.c
    src/touch.c
    src/ble.c
    src/wifi.c
    src/mqtt.c
    src/http.c
    ${DEVICE_DRIVERS}
)

# Include the shared Volt headers
target_include_directories(volt-rp2040 PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/../include
)

# Link pico SDK libraries
target_link_libraries(volt-rp2040
    pico_stdlib
    hardware_gpio
    hardware_pwm
    hardware_i2c
    hardware_spi
    hardware_uart
    hardware_timer
    hardware_adc
    hardware_watchdog
    pico_unique_id
)

# Enable USB serial output (not UART — Pico uses USB CDC)
pico_enable_stdio_usb(volt-rp2040 1)
pico_enable_stdio_uart(volt-rp2040 0)

# Generate .uf2 file for drag-and-drop flashing
pico_add_extra_outputs(volt-rp2040)
