#include "volt/storage.h"
#include "volt/log.h"

#include <string.h>
#include "nvs_flash.h"
#include "nvs.h"
#include "esp_timer.h"
#include "esp_system.h"
#include "esp_event.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/semphr.h"

// Mutex: protects s_dirty[] and s_lists[] from concurrent access.
// The flush timer runs on the esp_timer task while user code runs
// on other tasks — without this lock, a push during flush corrupts data.
static SemaphoreHandle_t s_mutex = NULL;

#define STORAGE_LOCK()   do { if (s_mutex) xSemaphoreTakeRecursive(s_mutex, portMAX_DELAY); } while(0)
#define STORAGE_UNLOCK() do { if (s_mutex) xSemaphoreGiveRecursive(s_mutex); } while(0)

// All Volt storage goes into one NVS namespace
#define VOLT_NVS_NAMESPACE "volt"

// Write coalescing: dirty values are buffered in RAM and flushed
// to flash every FLUSH_INTERVAL_MS or on explicit storage_flush().
#define FLUSH_INTERVAL_MS 30000
#define MAX_DIRTY_ENTRIES 32

// Boot loop detection: if the device crashes (panic / watchdog) more than
// MAX_BOOT_CRASHES times in a row without staying alive for MIN_STABLE_MS
// after boot, trigger factory reset. Power-on, reset-button, flashing and
// brownout boots don't count — erasing storage can't fix those.
#define MAX_BOOT_CRASHES 5
#define MIN_STABLE_MS    60000

// ============================================================
// Write coalescing buffer
// ============================================================

typedef enum {
    DIRTY_INT,
    DIRTY_FLOAT,
    DIRTY_STR,
} dirty_type_t;

typedef struct {
    char key[16];           // NVS max key is 15 chars + null
    dirty_type_t type;
    union {
        int    i;
        float  f;
        char   s[256];
    } value;
    bool active;            // slot in use
} dirty_entry_t;

static dirty_entry_t s_dirty[MAX_DIRTY_ENTRIES];
static bool s_initialized = false;
static esp_timer_handle_t s_flush_timer = NULL;

// ============================================================
// Store-and-forward list types (needed before storage_flush)
// ============================================================

#define MAX_LISTS 8
#define LIST_NVS_PREFIX "_l_"
#define STORAGE_LIST_MAX 64

typedef struct {
    char name[12];
    int32_t ring[STORAGE_LIST_MAX];
    int head;
    int count;
    bool active;
} storage_list_t;

static storage_list_t s_lists[MAX_LISTS];

// Forward declarations
static void list_flush(storage_list_t *list);
static int list_load_nvs(const char *name, int32_t *buf, int buf_size);

// ============================================================
// Shutdown handler
// ============================================================

static void shutdown_flush_handler(void)
{
    if (s_mutex && xSemaphoreTakeRecursive(s_mutex, pdMS_TO_TICKS(500)) == pdTRUE) {
        nvs_handle_t handle;
        if (nvs_open(VOLT_NVS_NAMESPACE, NVS_READWRITE, &handle) == ESP_OK) {
            for (int i = 0; i < MAX_DIRTY_ENTRIES; i++) {
                if (!s_dirty[i].active) continue;
                switch (s_dirty[i].type) {
                    case DIRTY_INT:
                        nvs_set_i32(handle, s_dirty[i].key, s_dirty[i].value.i);
                        break;
                    case DIRTY_FLOAT:
                        nvs_set_blob(handle, s_dirty[i].key, &s_dirty[i].value.f, sizeof(float));
                        break;
                    case DIRTY_STR:
                        nvs_set_str(handle, s_dirty[i].key, s_dirty[i].value.s);
                        break;
                }
                s_dirty[i].active = false;
            }
            nvs_commit(handle);
            nvs_close(handle);
        }
        xSemaphoreGiveRecursive(s_mutex);
    }
}

// ============================================================
// Internal helpers
// ============================================================

// Open NVS handle, perform operation, close. Returns ESP_OK or error.
static esp_err_t nvs_open_handle(nvs_handle_t *handle, nvs_open_mode_t mode)
{
    return nvs_open(VOLT_NVS_NAMESPACE, mode, handle);
}

// Find existing dirty entry for a key, or find an empty slot
static dirty_entry_t *find_dirty_slot(const char *key)
{
    dirty_entry_t *empty = NULL;

    for (int i = 0; i < MAX_DIRTY_ENTRIES; i++) {
        if (s_dirty[i].active && strcmp(s_dirty[i].key, key) == 0) {
            return &s_dirty[i];  // existing entry for this key
        }
        if (!s_dirty[i].active && empty == NULL) {
            empty = &s_dirty[i]; // first empty slot
        }
    }

    return empty;  // NULL if buffer is full
}

// Flush timer callback — writes all dirty values to flash
static void flush_timer_callback(void *arg)
{
    storage_flush();
}

// ============================================================
// Boot loop detection
// ============================================================

static void check_boot_loop(void)
{
    nvs_handle_t handle;
    if (nvs_open_handle(&handle, NVS_READWRITE) != ESP_OK) return;

    // Read crash counter
    int32_t crash_count = 0;
    nvs_get_i32(handle, "_crashes", &crash_count);

    esp_reset_reason_t reason = esp_reset_reason();
    bool crashed = reason == ESP_RST_PANIC || reason == ESP_RST_INT_WDT ||
                   reason == ESP_RST_TASK_WDT || reason == ESP_RST_WDT;
    if (!crashed) {
        if (crash_count != 0) {
            nvs_set_i32(handle, "_crashes", 0);
            nvs_commit(handle);
        }
        nvs_close(handle);
        return;
    }

    if (crash_count >= MAX_BOOT_CRASHES) {
        log_error("storage", "boot loop detected (%ld crashes) — factory reset", (long)crash_count);
        nvs_close(handle);
        storage_factory_reset();
        return;  // never reaches here — factory_reset reboots
    }

    // Increment crash counter (cap at MAX_BOOT_CRASHES to prevent overflow bypass)
    if (crash_count < MAX_BOOT_CRASHES) crash_count++;
    nvs_set_i32(handle, "_crashes", crash_count);
    nvs_commit(handle);
    nvs_close(handle);

    log_debug("storage", "boot count: %ld (resets after %ds stable)",
              (long)crash_count, MIN_STABLE_MS / 1000);

    // Crash counter is cleared in storage_flush() after MIN_STABLE_MS
}

// Clear the crash counter — called once the device has been stable
static void clear_crash_counter(void)
{
    nvs_handle_t handle;
    if (nvs_open_handle(&handle, NVS_READWRITE) != ESP_OK) return;
    nvs_set_i32(handle, "_crashes", 0);
    nvs_commit(handle);
    nvs_close(handle);
    log_debug("storage", "boot stable — crash counter cleared");
}

// ============================================================
// Public API
// ============================================================

volt_error_t storage_init(void)
{
    if (s_initialized) return VOLT_OK;

    // Initialize NVS flash
    esp_err_t err = nvs_flash_init();
    if (err == ESP_ERR_NVS_NO_FREE_PAGES || err == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        log_warn("storage", "NVS partition issue — erasing and reinitializing");
        nvs_flash_erase();
        err = nvs_flash_init();
    }
    if (err != ESP_OK) {
        log_error("storage", "NVS init failed (esp_err=%d)", err);
        return VOLT_ERR_STORAGE_INIT_FAILED;
    }

    // Recursive mutex: storage_save_* can call storage_flush() internally
    // when the dirty buffer is full, and flush is also called by the timer.
    if (s_mutex == NULL) {
        s_mutex = xSemaphoreCreateRecursiveMutex();
    }

    // Clear dirty buffer and lists
    memset(s_dirty, 0, sizeof(s_dirty));
    memset(s_lists, 0, sizeof(s_lists));

    // Boot loop detection
    check_boot_loop();

    // Start flush timer — writes dirty values to flash every 30 seconds
    esp_timer_create_args_t timer_args = {
        .callback = flush_timer_callback,
        .name = "nvs_flush",
    };
    esp_timer_create(&timer_args, &s_flush_timer);
    esp_timer_start_periodic(s_flush_timer, FLUSH_INTERVAL_MS * 1000);

    // Register shutdown handler — flushes dirty data before reboot/OTA/brownout.
    // esp_register_shutdown_handler() is not available on ESP8266 RTOS SDK;
    // on that platform the flush timer and explicit storage_flush() calls suffice.
#ifndef CONFIG_IDF_TARGET_ESP8266
    esp_register_shutdown_handler(shutdown_flush_handler);
#endif

    s_initialized = true;

    // Log health on startup
    storage_health_t health = storage_get_health();
    log_info("storage", "initialized — %lu/%lu entries used (%d%%)",
             (unsigned long)health.used_entries,
             (unsigned long)(health.used_entries + health.free_entries),
             health.usage_pct);

    return VOLT_OK;
}

volt_error_t storage_save_int(const char *key, int value)
{
    if (!s_initialized) storage_init();
    if (key == NULL || strlen(key) > 15) return VOLT_ERR_INVALID_ARG;

    STORAGE_LOCK();
    dirty_entry_t *slot = find_dirty_slot(key);
    if (slot == NULL) {
        storage_flush();
        slot = find_dirty_slot(key);
        if (slot == NULL) { STORAGE_UNLOCK(); return VOLT_ERR_STORAGE_FULL; }
    }

    strncpy(slot->key, key, 15);
    slot->key[15] = '\0';
    slot->type = DIRTY_INT;
    slot->value.i = value;
    slot->active = true;
    STORAGE_UNLOCK();

    return VOLT_OK;
}

int storage_load_int(const char *key, int default_val)
{
    if (!s_initialized) storage_init();
    if (key == NULL) return default_val;

    STORAGE_LOCK();
    // Check dirty buffer first — may have unflushed value
    for (int i = 0; i < MAX_DIRTY_ENTRIES; i++) {
        if (s_dirty[i].active && s_dirty[i].type == DIRTY_INT &&
            strcmp(s_dirty[i].key, key) == 0) {
            int result = s_dirty[i].value.i;
            STORAGE_UNLOCK();
            return result;
        }
    }
    STORAGE_UNLOCK();

    // NVS read doesn't need the lock — NVS has its own internal protection
    nvs_handle_t handle;
    if (nvs_open_handle(&handle, NVS_READONLY) != ESP_OK) return default_val;

    int32_t value;
    esp_err_t err = nvs_get_i32(handle, key, &value);
    nvs_close(handle);

    return (err == ESP_OK) ? (int)value : default_val;
}

volt_error_t storage_save_float(const char *key, float value)
{
    if (!s_initialized) storage_init();
    if (key == NULL || strlen(key) > 15) return VOLT_ERR_INVALID_ARG;

    STORAGE_LOCK();
    dirty_entry_t *slot = find_dirty_slot(key);
    if (slot == NULL) {
        storage_flush();
        slot = find_dirty_slot(key);
        if (slot == NULL) { STORAGE_UNLOCK(); return VOLT_ERR_STORAGE_FULL; }
    }

    strncpy(slot->key, key, 15);
    slot->key[15] = '\0';
    slot->type = DIRTY_FLOAT;
    slot->value.f = value;
    slot->active = true;
    STORAGE_UNLOCK();

    return VOLT_OK;
}

float storage_load_float(const char *key, float default_val)
{
    if (!s_initialized) storage_init();
    if (key == NULL) return default_val;

    STORAGE_LOCK();
    for (int i = 0; i < MAX_DIRTY_ENTRIES; i++) {
        if (s_dirty[i].active && s_dirty[i].type == DIRTY_FLOAT &&
            strcmp(s_dirty[i].key, key) == 0) {
            float result = s_dirty[i].value.f;
            STORAGE_UNLOCK();
            return result;
        }
    }
    STORAGE_UNLOCK();

    nvs_handle_t handle;
    if (nvs_open_handle(&handle, NVS_READONLY) != ESP_OK) return default_val;

    float value;
    size_t len = sizeof(float);
    esp_err_t err = nvs_get_blob(handle, key, &value, &len);
    nvs_close(handle);

    return (err == ESP_OK) ? value : default_val;
}

volt_error_t storage_save_str(const char *key, const char *value)
{
    if (!s_initialized) storage_init();
    if (key == NULL || value == NULL || strlen(key) > 15) return VOLT_ERR_INVALID_ARG;

    STORAGE_LOCK();
    dirty_entry_t *slot = find_dirty_slot(key);
    if (slot == NULL) {
        storage_flush();
        slot = find_dirty_slot(key);
        if (slot == NULL) { STORAGE_UNLOCK(); return VOLT_ERR_STORAGE_FULL; }
    }

    strncpy(slot->key, key, 15);
    slot->key[15] = '\0';
    slot->type = DIRTY_STR;
    size_t val_len = strlen(value);
    if (val_len >= sizeof(slot->value.s)) {
        log_warn("storage", "value for '%s' truncated (%zu → %zu bytes)",
                 key, val_len, sizeof(slot->value.s) - 1);
    }
    strncpy(slot->value.s, value, sizeof(slot->value.s) - 1);
    slot->value.s[sizeof(slot->value.s) - 1] = '\0';
    slot->active = true;
    STORAGE_UNLOCK();

    return VOLT_OK;
}

volt_error_t storage_load_str(const char *key, char *buf, size_t buf_size,
                               const char *default_val)
{
    if (!s_initialized) storage_init();
    if (key == NULL || buf == NULL || buf_size == 0) return VOLT_ERR_INVALID_ARG;

    STORAGE_LOCK();
    for (int i = 0; i < MAX_DIRTY_ENTRIES; i++) {
        if (s_dirty[i].active && s_dirty[i].type == DIRTY_STR &&
            strcmp(s_dirty[i].key, key) == 0) {
            strncpy(buf, s_dirty[i].value.s, buf_size - 1);
            buf[buf_size - 1] = '\0';
            STORAGE_UNLOCK();
            return VOLT_OK;
        }
    }
    STORAGE_UNLOCK();

    // Read from NVS
    nvs_handle_t handle;
    if (nvs_open_handle(&handle, NVS_READONLY) != ESP_OK) {
        if (default_val) strncpy(buf, default_val, buf_size - 1);
        buf[buf_size - 1] = '\0';
        return VOLT_ERR_KEY_NOT_FOUND;
    }

    size_t len = buf_size;
    esp_err_t err = nvs_get_str(handle, key, buf, &len);
    nvs_close(handle);

    if (err != ESP_OK) {
        if (default_val) strncpy(buf, default_val, buf_size - 1);
        buf[buf_size - 1] = '\0';
        return VOLT_ERR_KEY_NOT_FOUND;
    }

    return VOLT_OK;
}

volt_error_t storage_delete(const char *key)
{
    if (!s_initialized) storage_init();
    if (key == NULL) return VOLT_ERR_INVALID_ARG;

    STORAGE_LOCK();
    for (int i = 0; i < MAX_DIRTY_ENTRIES; i++) {
        if (s_dirty[i].active && strcmp(s_dirty[i].key, key) == 0) {
            s_dirty[i].active = false;
        }
    }
    STORAGE_UNLOCK();

    // Remove from NVS
    nvs_handle_t handle;
    if (nvs_open_handle(&handle, NVS_READWRITE) != ESP_OK) return VOLT_ERR_STORAGE_WRITE_FAILED;

    nvs_erase_key(handle, key);
    nvs_commit(handle);
    nvs_close(handle);

    return VOLT_OK;
}

bool storage_exists(const char *key)
{
    if (!s_initialized) storage_init();
    if (key == NULL) return false;

    STORAGE_LOCK();
    for (int i = 0; i < MAX_DIRTY_ENTRIES; i++) {
        if (s_dirty[i].active && strcmp(s_dirty[i].key, key) == 0) {
            STORAGE_UNLOCK();
            return true;
        }
    }
    STORAGE_UNLOCK();

    nvs_handle_t handle;
    if (nvs_open_handle(&handle, NVS_READONLY) != ESP_OK) return false;

#ifdef CONFIG_IDF_TARGET_ESP8266
    // nvs_find_key() is ESP-IDF v5.3+ only and absent from ESP8266 RTOS SDK.
    // Probe existence by attempting to read as each supported type in turn.
    // Any return code other than ESP_ERR_NVS_NOT_FOUND means the key exists.
    int32_t probe_i;
    esp_err_t err = nvs_get_i32(handle, key, &probe_i);
    if (err == ESP_ERR_NVS_NOT_FOUND) {
        // Not an i32 — try string
        size_t probe_len = 0;
        err = nvs_get_str(handle, key, NULL, &probe_len);
    }
    if (err == ESP_ERR_NVS_NOT_FOUND) {
        // Not a string — try blob (used for floats)
        size_t probe_len = 0;
        err = nvs_get_blob(handle, key, NULL, &probe_len);
    }
    nvs_close(handle);
    return (err != ESP_ERR_NVS_NOT_FOUND);
#else
    // nvs_find_key() checks existence without knowing the type (ESP-IDF v5.3+).
    nvs_type_t type;
    esp_err_t err = nvs_find_key(handle, key, &type);
    nvs_close(handle);
    return (err == ESP_OK);
#endif
}

volt_error_t storage_flush(void)
{
    if (!s_initialized) return VOLT_OK;

    STORAGE_LOCK();

    // Count dirty entries
    int dirty_count = 0;
    for (int i = 0; i < MAX_DIRTY_ENTRIES; i++) {
        if (s_dirty[i].active) dirty_count++;
    }
    if (dirty_count == 0) {
        // Still flush lists even if no dirty key-value entries
        for (int i = 0; i < MAX_LISTS; i++) {
            if (s_lists[i].active && s_lists[i].count > 0) {
                list_flush(&s_lists[i]);
            }
        }
        STORAGE_UNLOCK();
        return VOLT_OK;
    }

    nvs_handle_t handle;
    esp_err_t err = nvs_open_handle(&handle, NVS_READWRITE);
    if (err != ESP_OK) {
        log_error("storage", "flush failed — can't open NVS");
        STORAGE_UNLOCK();
        return VOLT_ERR_STORAGE_WRITE_FAILED;
    }

    int written = 0;
    for (int i = 0; i < MAX_DIRTY_ENTRIES; i++) {
        if (!s_dirty[i].active) continue;

        switch (s_dirty[i].type) {
            case DIRTY_INT:
                nvs_set_i32(handle, s_dirty[i].key, s_dirty[i].value.i);
                break;
            case DIRTY_FLOAT:
                nvs_set_blob(handle, s_dirty[i].key, &s_dirty[i].value.f, sizeof(float));
                break;
            case DIRTY_STR:
                nvs_set_str(handle, s_dirty[i].key, s_dirty[i].value.s);
                break;
        }

        s_dirty[i].active = false;
        written++;
    }

    nvs_commit(handle);
    nvs_close(handle);

    // Clear crash counter after stable operation
    // (measured from boot, not from the first flush)
    static bool crash_cleared = false;
    if (!crash_cleared && esp_timer_get_time() / 1000 > MIN_STABLE_MS) {
        clear_crash_counter();
        crash_cleared = true;
    }

    log_debug("storage", "flushed %d entries to flash", written);

    // Flush store-and-forward lists too
    for (int i = 0; i < MAX_LISTS; i++) {
        if (s_lists[i].active && s_lists[i].count > 0) {
            list_flush(&s_lists[i]);
        }
    }

    STORAGE_UNLOCK();
    return VOLT_OK;
}

storage_health_t storage_get_health(void)
{
    storage_health_t health = {0};

    nvs_stats_t stats;
    if (nvs_get_stats(VOLT_NVS_NAMESPACE, &stats) == ESP_OK) {
        health.used_entries = stats.used_entries;
        health.free_entries = stats.free_entries;
        uint32_t total = stats.used_entries + stats.free_entries;
        health.usage_pct = total > 0 ? (uint8_t)(100 * stats.used_entries / total) : 0;
    }

    return health;
}

// ============================================================
// Store-and-forward lists
// ============================================================
//
// Each named list is stored as:
//   - RAM: ring buffer of pending (unflushed) entries
//   - NVS: blob of int32_t values (flushed entries)
//
// On push: value goes into the RAM ring buffer.
// On flush: RAM entries are appended to the NVS blob.
// On drain: NVS blob + RAM entries are returned, list is cleared.
//
// This means pushes are free (no flash wear), and data survives
// reboots because the flush timer writes to NVS every 30 seconds.

// MAX_LISTS, LIST_NVS_PREFIX, storage_list_t, s_lists
// defined at the top of the file (needed by storage_flush)

// Find or create a list slot
static storage_list_t *find_list(const char *name, bool create)
{
    storage_list_t *empty = NULL;

    for (int i = 0; i < MAX_LISTS; i++) {
        if (s_lists[i].active && strcmp(s_lists[i].name, name) == 0) {
            return &s_lists[i];
        }
        if (!s_lists[i].active && empty == NULL) {
            empty = &s_lists[i];
        }
    }

    if (create && empty != NULL) {
        memset(empty, 0, sizeof(*empty));
        strncpy(empty->name, name, sizeof(empty->name) - 1);
        empty->active = true;
        return empty;
    }

    return NULL;
}

// Build the NVS key for a list: "_l_" + name
static void list_nvs_key(const char *name, char *out, size_t out_size)
{
    snprintf(out, out_size, "%s%.12s", LIST_NVS_PREFIX, name);
}

// Load existing entries from NVS blob into buf. Returns count.
static int list_load_nvs(const char *name, int32_t *buf, int buf_size)
{
    char key[16];
    list_nvs_key(name, key, sizeof(key));

    nvs_handle_t handle;
    if (nvs_open_handle(&handle, NVS_READONLY) != ESP_OK) return 0;

    size_t blob_size = 0;
    if (nvs_get_blob(handle, key, NULL, &blob_size) != ESP_OK || blob_size == 0) {
        nvs_close(handle);
        return 0;
    }

    int nvs_count = blob_size / sizeof(int32_t);
    if (nvs_count > buf_size) nvs_count = buf_size;

    nvs_get_blob(handle, key, buf, &blob_size);
    nvs_close(handle);

    return nvs_count;
}

// Flush a single list's RAM entries to NVS
static void list_flush(storage_list_t *list)
{
    if (list->count == 0) return;

    char key[16];
    list_nvs_key(list->name, key, sizeof(key));

    // Load existing NVS entries
    int32_t combined[STORAGE_LIST_MAX];
    int nvs_count = list_load_nvs(list->name, combined, STORAGE_LIST_MAX);

    // Append RAM entries
    int start = (list->head - list->count + STORAGE_LIST_MAX) % STORAGE_LIST_MAX;
    for (int i = 0; i < list->count && nvs_count < STORAGE_LIST_MAX; i++) {
        int idx = (start + i) % STORAGE_LIST_MAX;
        combined[nvs_count++] = list->ring[idx];
    }

    // If over limit, keep newest entries
    if (nvs_count > STORAGE_LIST_MAX) {
        int drop = nvs_count - STORAGE_LIST_MAX;
        memmove(combined, combined + drop, STORAGE_LIST_MAX * sizeof(int32_t));
        nvs_count = STORAGE_LIST_MAX;
    }

    // Write blob
    nvs_handle_t handle;
    if (nvs_open_handle(&handle, NVS_READWRITE) == ESP_OK) {
        nvs_set_blob(handle, key, combined, nvs_count * sizeof(int32_t));
        nvs_commit(handle);
        nvs_close(handle);
    }

    // Clear RAM buffer
    list->count = 0;
    list->head = 0;

    log_debug("storage", "list '%s' flushed — %d entries in NVS", list->name, nvs_count);
}

volt_error_t storage_push(const char *list_name, int value)
{
    if (!s_initialized) storage_init();
    if (list_name == NULL) return VOLT_ERR_INVALID_ARG;

    STORAGE_LOCK();
    storage_list_t *list = find_list(list_name, true);
    if (list == NULL) {
        STORAGE_UNLOCK();
        log_error("storage", "max lists reached (%d)", MAX_LISTS);
        return VOLT_ERR_STORAGE_FULL;
    }

    list->ring[list->head] = (int32_t)value;
    list->head = (list->head + 1) % STORAGE_LIST_MAX;
    if (list->count < STORAGE_LIST_MAX) {
        list->count++;
    }
    STORAGE_UNLOCK();

    return VOLT_OK;
}

int storage_list_count(const char *list_name)
{
    if (!s_initialized) storage_init();
    if (list_name == NULL) return 0;

    STORAGE_LOCK();
    int32_t tmp[STORAGE_LIST_MAX];
    int nvs_count = list_load_nvs(list_name, tmp, STORAGE_LIST_MAX);

    storage_list_t *list = find_list(list_name, false);
    int ram_count = list ? list->count : 0;
    STORAGE_UNLOCK();

    return nvs_count + ram_count;
}

int storage_drain(const char *list_name, int *buf, int buf_size)
{
    if (!s_initialized) storage_init();
    if (list_name == NULL || buf == NULL || buf_size <= 0) return 0;

    STORAGE_LOCK();
    int total = 0;

    // 1. Read NVS entries first (oldest data)
    int32_t nvs_buf[STORAGE_LIST_MAX];
    int nvs_count = list_load_nvs(list_name, nvs_buf, STORAGE_LIST_MAX);
    for (int i = 0; i < nvs_count && total < buf_size; i++) {
        buf[total++] = (int)nvs_buf[i];
    }

    // 2. Read RAM entries (newest data)
    storage_list_t *list = find_list(list_name, false);
    if (list != NULL && list->count > 0) {
        int start = (list->head - list->count + STORAGE_LIST_MAX) % STORAGE_LIST_MAX;
        for (int i = 0; i < list->count && total < buf_size; i++) {
            int idx = (start + i) % STORAGE_LIST_MAX;
            buf[total++] = (int)list->ring[idx];
        }
        list->count = 0;
        list->head = 0;
    }

    // 3. Clear NVS blob
    if (nvs_count > 0) {
        char key[16];
        list_nvs_key(list_name, key, sizeof(key));
        nvs_handle_t handle;
        if (nvs_open_handle(&handle, NVS_READWRITE) == ESP_OK) {
            nvs_erase_key(handle, key);
            nvs_commit(handle);
            nvs_close(handle);
        }
    }
    STORAGE_UNLOCK();

    log_info("storage", "drained %d entries from '%s'", total, list_name);
    return total;
}

volt_error_t storage_list_clear(const char *list_name)
{
    if (!s_initialized) storage_init();
    if (list_name == NULL) return VOLT_ERR_INVALID_ARG;

    STORAGE_LOCK();
    storage_list_t *list = find_list(list_name, false);
    if (list != NULL) {
        list->count = 0;
        list->head = 0;
    }
    STORAGE_UNLOCK();

    char key[16];
    list_nvs_key(list_name, key, sizeof(key));
    nvs_handle_t handle;
    if (nvs_open_handle(&handle, NVS_READWRITE) == ESP_OK) {
        nvs_erase_key(handle, key);
        nvs_commit(handle);
        nvs_close(handle);
    }

    return VOLT_OK;
}

// ============================================================

volt_error_t storage_factory_reset(void)
{
    log_warn("storage", "FACTORY RESET — erasing all data");

    // Drain any active NVS operations before erasing the partition.
    // Bounded timeout — if another task is mid-flush, wait up to 1 second.
    if (s_mutex) {
        xSemaphoreTakeRecursive(s_mutex, pdMS_TO_TICKS(1000));
        // Don't release — we're about to erase and reboot
    }

    nvs_flash_erase();
    log_warn("storage", "rebooting...");
    esp_restart();
    return VOLT_OK;  // never reaches here
}
