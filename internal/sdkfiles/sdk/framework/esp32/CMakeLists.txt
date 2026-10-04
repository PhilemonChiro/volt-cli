# Minimum CMake version ESP-IDF v5.4 requires
cmake_minimum_required(VERSION 3.16)

# Pull in ESP-IDF's build system — this one line gives us access to
# the entire ESP-IDF toolchain, components, and build infrastructure
include($ENV{IDF_PATH}/tools/cmake/project.cmake)

# Our project name — appears in build logs and binary metadata
project(volt-framework)
