#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <stdio.h>
#include "volt/storage.h"
#include "volt/log.h"
#include "pico/stdlib.h"
#include "hardware/flash.h"
#include "hardware/sync.h"
#include "hardware/watchdog.h"

// ============================================================
// RP2040 Flash-based Key-Value Storage
//
// Uses the last 2 sectors (8KB) of flash for persistent storage.
// Sector A and Sector B alternate — write to the clean one,
// erase the old one. This gives basic wear-leveling and ensures
// a power-cut mid-write doesn't lose data (the old sector is
// still intact until the new one is fully written).
//
// Layout per sector:
//   [0..3]   magic (0x564F4C54 = "VOLT")
//   [4..7]   sequence number (higher = newer)
//   [8..N]   entries: [key_len(1) | key(15) | type(1) | value(4 or 256)]
//   [N..end] 0xFF (erased)
//
// Dirty buffer in RAM coalesces writes. Flushed on timer or
// explicit storage_flush() — same pattern as ESP32.
// ============================================================

// RP2040: 2MB flash, last 8KB reserved for storage
// XIP_BASE is 0x10000000, flash starts there
#define FLASH_STORAGE_SIZE  (2 * FLASH_SECTOR_SIZE)  // 8KB (2 x 4KB sectors)
#define FLASH_STORAGE_OFFSET (PICO_FLASH_SIZE_BYTES - FLASH_STORAGE_SIZE)
#define SECTOR_A_OFFSET     FLASH_STORAGE_OFFSET
#define SECTOR_B_OFFSET     (FLASH_STORAGE_OFFSET + FLASH_SECTOR_SIZE)

#define STORAGE_MAGIC       0x564F4C54  // "VOLT"
#define MAX_KEY_LEN         15
#define MAX_STR_LEN         255

// Spinlock for protecting flash operations between cores
static spin_lock_t *s_flash_lock = NULL;

// Entry types
#define ENTRY_INT   1
#define ENTRY_FLOAT 2
#define ENTRY_STR   3
#define ENTRY_END   0xFF  // erased flash = no more entries

// On-flash entry: 20 bytes for int/float, up to 276 for string
typedef struct __attribute__((packed)) {
    uint8_t  key_len;        // 0xFF = end marker
    char     key[MAX_KEY_LEN];
    uint8_t  type;           // ENTRY_INT, ENTRY_FLOAT, ENTRY_STR
    union {
        int32_t i;
        float   f;
        struct {
            uint8_t len;
            char    data[MAX_STR_LEN];
        } s;
    } value;
} flash_entry_t;

#define ENTRY_HEADER_SIZE  (1 + MAX_KEY_LEN + 1)  // key_len + key + type
#define ENTRY_INT_SIZE     (ENTRY_HEADER_SIZE + 4)
#define ENTRY_FLOAT_SIZE   (ENTRY_HEADER_SIZE + 4)

// Sector header
typedef struct __attribute__((packed)) {
    uint32_t magic;
    uint32_t sequence;
} sector_header_t;

#define SECTOR_DATA_OFFSET sizeof(sector_header_t)
#define SECTOR_DATA_SIZE   (FLASH_SECTOR_SIZE - SECTOR_DATA_OFFSET)

// ============================================================
// RAM dirty buffer (same pattern as ESP32)
// ============================================================

#define MAX_DIRTY 32

typedef enum { DIRTY_INT, DIRTY_FLOAT, DIRTY_STR } dirty_type_t;

typedef struct {
    char key[MAX_KEY_LEN + 1];
    dirty_type_t type;
    union {
        int32_t i;
        float   f;
        char    s[MAX_STR_LEN + 1];
    } value;
    bool active;
} dirty_entry_t;

static dirty_entry_t s_dirty[MAX_DIRTY];
static bool s_initialized = false;

// Store-and-forward lists
#define MAX_LISTS 8

typedef struct {
    char name[12];
    int32_t ring[STORAGE_LIST_MAX];
    int head;
    int count;
    bool active;
} storage_list_t;

static storage_list_t s_lists[MAX_LISTS];

// Flush timer
static repeating_timer_t s_flush_timer;
#define FLUSH_INTERVAL_MS 30000

// ============================================================
// Flash helpers
// ============================================================

static const uint8_t *flash_read_ptr(uint32_t offset)
{
    return (const uint8_t *)(XIP_BASE + offset);
}

static uint32_t get_sector_sequence(uint32_t sector_offset)
{
    const sector_header_t *hdr = (const sector_header_t *)flash_read_ptr(sector_offset);
    if (hdr->magic != STORAGE_MAGIC) return 0;
    return hdr->sequence;
}

// Returns the offset of the active (newest) sector
static uint32_t active_sector(void)
{
    uint32_t seq_a = get_sector_sequence(SECTOR_A_OFFSET);
    uint32_t seq_b = get_sector_sequence(SECTOR_B_OFFSET);
    return (seq_b > seq_a) ? SECTOR_B_OFFSET : SECTOR_A_OFFSET;
}

// Returns the offset of the inactive sector (for next write)
static uint32_t inactive_sector(void)
{
    uint32_t active = active_sector();
    return (active == SECTOR_A_OFFSET) ? SECTOR_B_OFFSET : SECTOR_A_OFFSET;
}

// Read a key from flash. Returns true if found.
static bool flash_read_entry(const char *key, dirty_type_t *type,
                             int32_t *out_i, float *out_f, char *out_s)
{
    uint32_t sector = active_sector();
    const uint8_t *base = flash_read_ptr(sector + SECTOR_DATA_OFFSET);
    uint32_t pos = 0;

    while (pos < SECTOR_DATA_SIZE) {
        uint8_t key_len = base[pos];
        if (key_len == 0xFF || key_len == 0) break;  // end of entries

        const char *entry_key = (const char *)&base[pos + 1];
        uint8_t entry_type = base[pos + 1 + MAX_KEY_LEN];

        bool match = (strlen(key) == key_len) &&
                     (memcmp(key, entry_key, key_len) == 0);

        // Calculate entry size to advance position
        uint32_t entry_size = ENTRY_HEADER_SIZE;
        if (entry_type == ENTRY_INT || entry_type == ENTRY_FLOAT) {
            entry_size += 4;
        } else if (entry_type == ENTRY_STR) {
            uint8_t slen = base[pos + ENTRY_HEADER_SIZE];
            entry_size += 1 + slen;
        } else {
            break;  // corrupt
        }

        if (match) {
            // Found — but keep scanning: later entries override earlier ones
            const uint8_t *val_ptr = &base[pos + ENTRY_HEADER_SIZE];
            if (entry_type == ENTRY_INT) {
                if (type) *type = DIRTY_INT;
                if (out_i) memcpy(out_i, val_ptr, 4);
            } else if (entry_type == ENTRY_FLOAT) {
                if (type) *type = DIRTY_FLOAT;
                if (out_f) memcpy(out_f, val_ptr, 4);
            } else if (entry_type == ENTRY_STR) {
                if (type) *type = DIRTY_STR;
                if (out_s) {
                    uint8_t slen = val_ptr[0];
                    memcpy(out_s, &val_ptr[1], slen);
                    out_s[slen] = '\0';
                }
            }
        }

        pos += entry_size;
    }

    // If we found at least one match, type was set
    return (type && *type != 0xFF);
}

// Write all current state to the inactive sector, then make it active
static void flash_commit_all(void)
{
    // Build the new sector image in RAM
    static uint8_t page_buf[FLASH_SECTOR_SIZE] __attribute__((aligned(256)));
    memset(page_buf, 0xFF, sizeof(page_buf));

    // Header
    uint32_t old_seq = get_sector_sequence(active_sector());
    sector_header_t hdr = { .magic = STORAGE_MAGIC, .sequence = old_seq + 1 };
    memcpy(page_buf, &hdr, sizeof(hdr));

    uint32_t pos = SECTOR_DATA_OFFSET;

    // First: copy non-dirty entries from the old active sector
    uint32_t old_sector = active_sector();
    const uint8_t *old_base = flash_read_ptr(old_sector + SECTOR_DATA_OFFSET);
    uint32_t old_pos = 0;

    while (old_pos < SECTOR_DATA_SIZE) {
        uint8_t key_len = old_base[old_pos];
        if (key_len == 0xFF || key_len == 0) break;

        const char *entry_key = (const char *)&old_base[old_pos + 1];
        uint8_t entry_type = old_base[old_pos + 1 + MAX_KEY_LEN];

        uint32_t entry_size = ENTRY_HEADER_SIZE;
        if (entry_type == ENTRY_INT || entry_type == ENTRY_FLOAT) {
            entry_size += 4;
        } else if (entry_type == ENTRY_STR) {
            uint8_t slen = old_base[old_pos + ENTRY_HEADER_SIZE];
            entry_size += 1 + slen;
        } else {
            break;
        }

        // Skip if this key is in the dirty buffer (dirty version takes priority)
        bool is_dirty = false;
        for (int i = 0; i < MAX_DIRTY; i++) {
            if (s_dirty[i].active && strlen(s_dirty[i].key) == key_len &&
                memcmp(s_dirty[i].key, entry_key, key_len) == 0) {
                is_dirty = true;
                break;
            }
        }

        if (!is_dirty && pos + entry_size < FLASH_SECTOR_SIZE) {
            memcpy(&page_buf[pos], &old_base[old_pos], entry_size);
            pos += entry_size;
        }

        old_pos += entry_size;
    }

    // Then: write all dirty entries
    for (int i = 0; i < MAX_DIRTY; i++) {
        if (!s_dirty[i].active) continue;

        uint8_t key_len = strlen(s_dirty[i].key);
        uint32_t entry_size = ENTRY_HEADER_SIZE;

        if (s_dirty[i].type == DIRTY_INT || s_dirty[i].type == DIRTY_FLOAT) {
            entry_size += 4;
        } else if (s_dirty[i].type == DIRTY_STR) {
            entry_size += 1 + strlen(s_dirty[i].value.s);
        }

        if (pos + entry_size >= FLASH_SECTOR_SIZE) {
            log_warn("storage", "sector full — dropping key '%s'", s_dirty[i].key);
            continue;
        }

        // key_len
        page_buf[pos] = key_len;
        // key (zero-padded to MAX_KEY_LEN)
        memset(&page_buf[pos + 1], 0, MAX_KEY_LEN);
        memcpy(&page_buf[pos + 1], s_dirty[i].key, key_len);

        if (s_dirty[i].type == DIRTY_INT) {
            page_buf[pos + 1 + MAX_KEY_LEN] = ENTRY_INT;
            memcpy(&page_buf[pos + ENTRY_HEADER_SIZE], &s_dirty[i].value.i, 4);
        } else if (s_dirty[i].type == DIRTY_FLOAT) {
            page_buf[pos + 1 + MAX_KEY_LEN] = ENTRY_FLOAT;
            memcpy(&page_buf[pos + ENTRY_HEADER_SIZE], &s_dirty[i].value.f, 4);
        } else if (s_dirty[i].type == DIRTY_STR) {
            page_buf[pos + 1 + MAX_KEY_LEN] = ENTRY_STR;
            uint8_t slen = strlen(s_dirty[i].value.s);
            page_buf[pos + ENTRY_HEADER_SIZE] = slen;
            memcpy(&page_buf[pos + ENTRY_HEADER_SIZE + 1], s_dirty[i].value.s, slen);
        }

        pos += entry_size;
        s_dirty[i].active = false;
    }

    // Write to flash — protect against concurrent access from other core
    // Split erase and program to minimize interrupt-disable time.
    // Erase (~4ms per 4KB on RP2040) and program (~5ms) each need interrupts OFF.
    uint32_t target = inactive_sector();
    uint32_t ints = save_and_disable_interrupts();
    uint32_t lock_num = spin_lock_get_num(s_flash_lock);
    spin_lock_blocking(s_flash_lock);
    flash_range_erase(target, FLASH_SECTOR_SIZE);
    spin_unlock(s_flash_lock, lock_num);
    restore_interrupts(ints);

    // Re-disable for the program step (shorter than erase)
    ints = save_and_disable_interrupts();
    spin_lock_blocking(s_flash_lock);
    flash_range_program(target, page_buf, FLASH_SECTOR_SIZE);
    spin_unlock(s_flash_lock, lock_num);
    restore_interrupts(ints);
}

// ============================================================
// Dirty buffer helpers
// ============================================================

static dirty_entry_t *find_dirty_slot(const char *key)
{
    dirty_entry_t *empty = NULL;
    for (int i = 0; i < MAX_DIRTY; i++) {
        if (s_dirty[i].active && strcmp(s_dirty[i].key, key) == 0)
            return &s_dirty[i];
        if (!s_dirty[i].active && empty == NULL)
            empty = &s_dirty[i];
    }
    return empty;
}

// ============================================================
// Flush timer callback
// ============================================================

static bool flush_timer_callback(repeating_timer_t *t)
{
    storage_flush();
    return true;  // keep repeating
}

// ============================================================
// Public API
// ============================================================

volt_error_t storage_init(void)
{
    if (s_initialized) return VOLT_OK;

    // Claim an unused spinlock for flash protection between cores
    s_flash_lock = spin_lock_claim_unused(true);

    memset(s_dirty, 0, sizeof(s_dirty));
    memset(s_lists, 0, sizeof(s_lists));

    // Start periodic flush timer (30 seconds)
    add_repeating_timer_ms(FLUSH_INTERVAL_MS, flush_timer_callback, NULL, &s_flush_timer);

    s_initialized = true;
    log_info("storage", "initialized (flash sectors at 0x%08X)", FLASH_STORAGE_OFFSET);
    return VOLT_OK;
}

volt_error_t storage_save_int(const char *key, int value)
{
    if (!s_initialized) storage_init();
    if (key == NULL || strlen(key) > MAX_KEY_LEN) return VOLT_ERR_INVALID_ARG;

    dirty_entry_t *slot = find_dirty_slot(key);
    if (slot == NULL) {
        storage_flush();
        slot = find_dirty_slot(key);
        if (slot == NULL) return VOLT_ERR_STORAGE_FULL;
    }

    strncpy(slot->key, key, MAX_KEY_LEN);
    slot->key[MAX_KEY_LEN] = '\0';
    slot->type = DIRTY_INT;
    slot->value.i = value;
    slot->active = true;
    return VOLT_OK;
}

int storage_load_int(const char *key, int default_val)
{
    if (!s_initialized) storage_init();
    if (key == NULL) return default_val;

    // Check dirty buffer first
    for (int i = 0; i < MAX_DIRTY; i++) {
        if (s_dirty[i].active && s_dirty[i].type == DIRTY_INT &&
            strcmp(s_dirty[i].key, key) == 0)
            return s_dirty[i].value.i;
    }

    // Read from flash
    dirty_type_t type = 0xFF;
    int32_t val;
    if (flash_read_entry(key, &type, &val, NULL, NULL) && type == DIRTY_INT)
        return val;

    return default_val;
}

volt_error_t storage_save_float(const char *key, float value)
{
    if (!s_initialized) storage_init();
    if (key == NULL || strlen(key) > MAX_KEY_LEN) return VOLT_ERR_INVALID_ARG;

    dirty_entry_t *slot = find_dirty_slot(key);
    if (slot == NULL) {
        storage_flush();
        slot = find_dirty_slot(key);
        if (slot == NULL) return VOLT_ERR_STORAGE_FULL;
    }

    strncpy(slot->key, key, MAX_KEY_LEN);
    slot->key[MAX_KEY_LEN] = '\0';
    slot->type = DIRTY_FLOAT;
    slot->value.f = value;
    slot->active = true;
    return VOLT_OK;
}

float storage_load_float(const char *key, float default_val)
{
    if (!s_initialized) storage_init();
    if (key == NULL) return default_val;

    for (int i = 0; i < MAX_DIRTY; i++) {
        if (s_dirty[i].active && s_dirty[i].type == DIRTY_FLOAT &&
            strcmp(s_dirty[i].key, key) == 0)
            return s_dirty[i].value.f;
    }

    dirty_type_t type = 0xFF;
    float val;
    if (flash_read_entry(key, &type, NULL, &val, NULL) && type == DIRTY_FLOAT)
        return val;

    return default_val;
}

volt_error_t storage_save_str(const char *key, const char *value)
{
    if (!s_initialized) storage_init();
    if (key == NULL || value == NULL || strlen(key) > MAX_KEY_LEN) return VOLT_ERR_INVALID_ARG;

    dirty_entry_t *slot = find_dirty_slot(key);
    if (slot == NULL) {
        storage_flush();
        slot = find_dirty_slot(key);
        if (slot == NULL) return VOLT_ERR_STORAGE_FULL;
    }

    strncpy(slot->key, key, MAX_KEY_LEN);
    slot->key[MAX_KEY_LEN] = '\0';
    slot->type = DIRTY_STR;
    strncpy(slot->value.s, value, MAX_STR_LEN);
    slot->value.s[MAX_STR_LEN] = '\0';
    slot->active = true;
    return VOLT_OK;
}

volt_error_t storage_load_str(const char *key, char *buf, size_t buf_size,
                               const char *default_val)
{
    if (!s_initialized) storage_init();
    if (key == NULL || buf == NULL || buf_size == 0) return VOLT_ERR_INVALID_ARG;

    for (int i = 0; i < MAX_DIRTY; i++) {
        if (s_dirty[i].active && s_dirty[i].type == DIRTY_STR &&
            strcmp(s_dirty[i].key, key) == 0) {
            strncpy(buf, s_dirty[i].value.s, buf_size - 1);
            buf[buf_size - 1] = '\0';
            return VOLT_OK;
        }
    }

    dirty_type_t type = 0xFF;
    char tmp[MAX_STR_LEN + 1];
    if (flash_read_entry(key, &type, NULL, NULL, tmp) && type == DIRTY_STR) {
        strncpy(buf, tmp, buf_size - 1);
        buf[buf_size - 1] = '\0';
        return VOLT_OK;
    }

    if (default_val) {
        strncpy(buf, default_val, buf_size - 1);
        buf[buf_size - 1] = '\0';
    }
    return VOLT_ERR_KEY_NOT_FOUND;
}

volt_error_t storage_delete(const char *key)
{
    if (!s_initialized) storage_init();
    if (key == NULL) return VOLT_ERR_INVALID_ARG;

    // Remove from dirty buffer
    for (int i = 0; i < MAX_DIRTY; i++) {
        if (s_dirty[i].active && strcmp(s_dirty[i].key, key) == 0)
            s_dirty[i].active = false;
    }

    // Flush to commit the deletion (entry will be absent from new sector)
    storage_flush();
    return VOLT_OK;
}

bool storage_exists(const char *key)
{
    if (!s_initialized) storage_init();
    if (key == NULL) return false;

    // Check dirty buffer
    for (int i = 0; i < MAX_DIRTY; i++) {
        if (s_dirty[i].active && strcmp(s_dirty[i].key, key) == 0)
            return true;
    }

    // Check flash
    dirty_type_t type = 0xFF;
    return flash_read_entry(key, &type, NULL, NULL, NULL);
}

volt_error_t storage_flush(void)
{
    if (!s_initialized) return VOLT_OK;

    // Check if there's anything to flush
    bool has_dirty = false;
    for (int i = 0; i < MAX_DIRTY; i++) {
        if (s_dirty[i].active) { has_dirty = true; break; }
    }

    bool has_lists = false;
    for (int i = 0; i < MAX_LISTS; i++) {
        if (s_lists[i].active && s_lists[i].count > 0) { has_lists = true; break; }
    }

    if (!has_dirty && !has_lists) return VOLT_OK;

    // Flush lists into dirty buffer as individual entries with list prefix
    for (int i = 0; i < MAX_LISTS; i++) {
        if (!s_lists[i].active || s_lists[i].count == 0) continue;

        // Write each list entry to dirty buffer
        int start = (s_lists[i].head - s_lists[i].count + STORAGE_LIST_MAX) % STORAGE_LIST_MAX;
        for (int j = 0; j < s_lists[i].count; j++) {
            int idx = (start + j) % STORAGE_LIST_MAX;
            int32_t value = s_lists[i].ring[idx];

            // Build key like "listname_0", "listname_1", etc.
            char key[24];
            snprintf(key, sizeof(key), "_list_%s_%d", s_lists[i].name, j);

            dirty_entry_t *slot = find_dirty_slot(key);
            if (slot) {
                slot->active = true;
                slot->type = DIRTY_INT;
                strncpy(slot->key, key, MAX_KEY_LEN);
                slot->key[MAX_KEY_LEN] = '\0';
                slot->value.i = value;
            }
        }

        // Clear the list
        s_lists[i].count = 0;
        s_lists[i].head = 0;
        has_dirty = true;  // Lists were added to dirty buffer
    }

    if (has_dirty) {
        flash_commit_all();
        log_debug("storage", "flushed to flash");
    }

    return VOLT_OK;
}

storage_health_t storage_get_health(void)
{
    storage_health_t health = {0};

    uint32_t sector = active_sector();
    const uint8_t *base = flash_read_ptr(sector + SECTOR_DATA_OFFSET);
    uint32_t pos = 0;
    uint32_t count = 0;

    while (pos < SECTOR_DATA_SIZE) {
        uint8_t key_len = base[pos];
        if (key_len == 0xFF || key_len == 0) break;

        uint8_t entry_type = base[pos + 1 + MAX_KEY_LEN];
        uint32_t entry_size = ENTRY_HEADER_SIZE;
        if (entry_type == ENTRY_INT || entry_type == ENTRY_FLOAT) {
            entry_size += 4;
        } else if (entry_type == ENTRY_STR) {
            entry_size += 1 + base[pos + ENTRY_HEADER_SIZE];
        } else {
            break;
        }
        pos += entry_size;
        count++;
    }

    health.used_entries = count;
    health.free_entries = (SECTOR_DATA_SIZE - pos) / ENTRY_INT_SIZE;  // estimate
    uint32_t total = health.used_entries + health.free_entries;
    health.usage_pct = total > 0 ? (uint8_t)(100 * health.used_entries / total) : 0;
    return health;
}

// ============================================================
// Store-and-forward lists
// ============================================================

static storage_list_t *find_list(const char *name, bool create)
{
    storage_list_t *empty = NULL;
    for (int i = 0; i < MAX_LISTS; i++) {
        if (s_lists[i].active && strcmp(s_lists[i].name, name) == 0)
            return &s_lists[i];
        if (!s_lists[i].active && empty == NULL)
            empty = &s_lists[i];
    }
    if (create && empty) {
        memset(empty, 0, sizeof(*empty));
        strncpy(empty->name, name, sizeof(empty->name) - 1);
        empty->active = true;
        return empty;
    }
    return NULL;
}

volt_error_t storage_push(const char *list_name, int value)
{
    if (!s_initialized) storage_init();
    if (list_name == NULL) return VOLT_ERR_INVALID_ARG;

    storage_list_t *list = find_list(list_name, true);
    if (list == NULL) return VOLT_ERR_STORAGE_FULL;

    list->ring[list->head] = (int32_t)value;
    list->head = (list->head + 1) % STORAGE_LIST_MAX;
    if (list->count < STORAGE_LIST_MAX) list->count++;
    return VOLT_OK;
}

int storage_list_count(const char *list_name)
{
    if (!s_initialized) storage_init();
    if (list_name == NULL) return 0;

    storage_list_t *list = find_list(list_name, false);
    return list ? list->count : 0;
}

int storage_drain(const char *list_name, int *buf, int buf_size)
{
    if (!s_initialized) storage_init();
    if (list_name == NULL || buf == NULL || buf_size <= 0) return 0;

    storage_list_t *list = find_list(list_name, false);
    if (list == NULL || list->count == 0) return 0;

    int total = 0;
    int start = (list->head - list->count + STORAGE_LIST_MAX) % STORAGE_LIST_MAX;
    for (int i = 0; i < list->count && total < buf_size; i++) {
        int idx = (start + i) % STORAGE_LIST_MAX;
        buf[total++] = (int)list->ring[idx];
    }

    list->count = 0;
    list->head = 0;
    return total;
}

volt_error_t storage_list_clear(const char *list_name)
{
    if (!s_initialized) storage_init();
    if (list_name == NULL) return VOLT_ERR_INVALID_ARG;

    storage_list_t *list = find_list(list_name, false);
    if (list) {
        list->count = 0;
        list->head = 0;
    }
    return VOLT_OK;
}

volt_error_t storage_factory_reset(void)
{
    log_warn("storage", "FACTORY RESET — erasing storage sectors");

    // Erase each sector separately to avoid ~200ms with interrupts disabled.
    // Each 4KB erase takes ~100ms on RP2040.
    for (uint32_t off = SECTOR_A_OFFSET; off < FLASH_STORAGE_OFFSET + FLASH_STORAGE_SIZE; off += FLASH_SECTOR_SIZE) {
        uint32_t ints = save_and_disable_interrupts();
        flash_range_erase(off, FLASH_SECTOR_SIZE);
        restore_interrupts(ints);
        watchdog_update();
    }

    memset(s_dirty, 0, sizeof(s_dirty));
    memset(s_lists, 0, sizeof(s_lists));

    log_warn("storage", "done — rebooting");
    watchdog_reboot(0, 0, 0);
    return VOLT_OK;  // never reaches
}
