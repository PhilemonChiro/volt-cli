#ifndef VOLT_STORAGE_H
#define VOLT_STORAGE_H

#include <stdint.h>
#include <stddef.h>
#include <stdbool.h>
#include "volt/error.h"

// ============================================================
// Limits (static allocation — no heap fragmentation on 24/7 devices)
//
//   Keys:     up to 32 dirty entries buffered before flush
//   Strings:  up to 255 bytes per value (warns on truncation)
//   Lists:    up to 8 named lists, 64 entries each
//   Flush:    every 30 seconds or on explicit storage_flush()
// ============================================================

// Save an integer. Buffered in RAM — written to flash on flush or timer.
volt_error_t storage_save_int(const char *key, int value);

// Load an integer. Returns default_val if key doesn't exist.
int storage_load_int(const char *key, int default_val);

// Save a float.
volt_error_t storage_save_float(const char *key, float value);

// Load a float. Returns default_val if key doesn't exist.
float storage_load_float(const char *key, float default_val);

// Save a string.
volt_error_t storage_save_str(const char *key, const char *value);

// Load a string into buf. Copies default_val if key doesn't exist.
volt_error_t storage_load_str(const char *key, char *buf, size_t buf_size,
                               const char *default_val);

// Delete a key from storage.
volt_error_t storage_delete(const char *key);

// Check if a key exists.
bool storage_exists(const char *key);

// Force write all dirty values to flash immediately.
// Called automatically every 30 seconds and on graceful shutdown.
volt_error_t storage_flush(void);

// Health monitoring — how full is the storage partition.
typedef struct {
    uint32_t used_entries;
    uint32_t free_entries;
    uint8_t  usage_pct;
} storage_health_t;

storage_health_t storage_get_health(void);

// ============================================================
// Store-and-forward lists
// ============================================================

// Maximum entries per named list.
#define STORAGE_LIST_MAX 64

// Push an int onto a named list. Buffered in RAM, flushed to NVS on timer.
// Oldest entries are dropped when the list is full.
volt_error_t storage_push(const char *list_name, int value);

// Number of entries currently in a list (RAM + NVS combined).
int storage_list_count(const char *list_name);

// Drain all entries from a named list into buf (up to buf_size entries).
// Returns number of entries copied. Clears the list after draining.
int storage_drain(const char *list_name, int *buf, int buf_size);

// Clear a named list without reading it.
volt_error_t storage_list_clear(const char *list_name);

// ============================================================

// Erase all stored data and reboot. Use for unrecoverable corruption.
volt_error_t storage_factory_reset(void);

// Initialize storage subsystem. Called by volt_init().
// Handles NVS init, boot loop detection, and starts the flush timer.
volt_error_t storage_init(void);

#endif // VOLT_STORAGE_H
