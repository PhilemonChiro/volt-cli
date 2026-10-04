# Register this component with ESP-IDF's build system
# Core framework sources (always included)
set(CORE_SRCS
    "main.c"
    "../src/error.c"
    "../src/log.c"
    "../src/pin.c"
    "../src/task.c"
    "../src/wifi.c"
    "../src/mqtt.c"
    "../src/storage.c"
    "../src/http.c"
    "../src/system.c"
    "../src/i2c.c"
    "../src/spi.c"
    "../src/uart.c"
)

# Discover device drivers, then compile ONLY the ones this program uses. The
# generated main.c calls <prefix>_init() and references <prefix>_t for every
# device it declares, so a driver whose prefix never appears in main.c isn't
# needed — and must NOT be compiled, or a single unused (or broken) installed
# driver would break every build.
file(GLOB ALL_DEVICE_DRIVERS "../../../devices/*/driver.c")
set(DEVICE_DRIVERS "")
set(_MAIN_C "${CMAKE_CURRENT_LIST_DIR}/main.c")
if(EXISTS "${_MAIN_C}")
    # Recompute the driver set whenever the program changes.
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
    set(DEVICE_DRIVERS ${ALL_DEVICE_DRIVERS})   # no compiled program yet — safe default
endif()

idf_component_register(
    SRCS ${CORE_SRCS} ${DEVICE_DRIVERS}
    INCLUDE_DIRS "../../include"
)
