#include "volt/ble.h"
#include "volt/log.h"

#include <string.h>
#include "esp_nimble_hci.h"
#include "nimble/nimble_port.h"
#include "nimble/nimble_port_freertos.h"
#include "host/ble_hs.h"
#include "host/util/util.h"
#include "services/gap/ble_svc_gap.h"
#include "services/gatt/ble_svc_gatt.h"

// ============================================================
// Internal state
// ============================================================

static bool ble_initialized = false;
static bool ble_connected = false;
static uint16_t conn_handle = 0;
static ble_write_callback_t write_cb = NULL;

// Value storage — each exposed value becomes a GATT characteristic
typedef enum { VAL_INT, VAL_FLOAT, VAL_STRING } val_type_t;

typedef struct {
    char name[16];
    val_type_t type;
    union {
        int32_t i;
        float f;
        char s[21];  // BLE MTU is typically 20 bytes
    } data;
    bool active;
    uint16_t val_handle;
} ble_value_t;

static ble_value_t values[BLE_MAX_VALUES];
static uint8_t value_count = 0;

// Scan results
#define MAX_SCAN_RESULTS 16
typedef struct {
    char name[32];
    int8_t rssi;
    bool has_name;
} scan_result_t;

static scan_result_t scan_results[MAX_SCAN_RESULTS];
static int scan_count = 0;

// ============================================================
// GATT characteristic access callback
// ============================================================

static int gatt_access_cb(uint16_t conn_handle, uint16_t attr_handle,
                          struct ble_gatt_access_ctxt *ctxt, void *arg)
{
    int idx = (int)(intptr_t)arg;
    if (idx < 0 || idx >= BLE_MAX_VALUES || !values[idx].active) return BLE_ATT_ERR_UNLIKELY;

    ble_value_t *v = &values[idx];

    if (ctxt->op == BLE_GATT_ACCESS_OP_READ_CHR) {
        // Read — return current value
        const void *data;
        uint16_t len;
        switch (v->type) {
            case VAL_INT:
                data = &v->data.i;
                len = sizeof(int32_t);
                break;
            case VAL_FLOAT:
                data = &v->data.f;
                len = sizeof(float);
                break;
            case VAL_STRING:
                data = v->data.s;
                len = strlen(v->data.s);
                break;
            default:
                return BLE_ATT_ERR_UNLIKELY;
        }
        int rc = os_mbuf_append(ctxt->om, data, len);
        return rc == 0 ? 0 : BLE_ATT_ERR_INSUFFICIENT_RES;
    }

    if (ctxt->op == BLE_GATT_ACCESS_OP_WRITE_CHR) {
        // Write — update value and notify callback
        uint16_t len = OS_MBUF_PKTLEN(ctxt->om);
        uint8_t buf[64];
        if (len > sizeof(buf)) len = sizeof(buf);
        os_mbuf_copydata(ctxt->om, 0, len, buf);

        if (write_cb) {
            write_cb(idx, buf, len);
        }
        return 0;
    }

    return BLE_ATT_ERR_UNLIKELY;
}

// ============================================================
// GAP event handler
// ============================================================

static int gap_event_cb(struct ble_gap_event *event, void *arg)
{
    switch (event->type) {
        case BLE_GAP_EVENT_CONNECT:
            if (event->connect.status == 0) {
                conn_handle = event->connect.conn_handle;
                ble_connected = true;
                log_info("ble", "connected");
            } else {
                ble_connected = false;
                // Re-advertise on failed connection
                ble_advertise(ble_svc_gap_device_name());
            }
            break;

        case BLE_GAP_EVENT_DISCONNECT:
            ble_connected = false;
            log_info("ble", "disconnected");
            // Re-advertise after disconnect
            ble_advertise(ble_svc_gap_device_name());
            break;

        case BLE_GAP_EVENT_DISC:
            // Scan result
            if (scan_count < MAX_SCAN_RESULTS) {
                scan_result_t *r = &scan_results[scan_count];
                r->rssi = event->disc.rssi;
                r->has_name = false;
                // Extract name from advertisement data
                struct ble_hs_adv_fields fields;
                if (ble_hs_adv_parse_fields(&fields, event->disc.data,
                                             event->disc.length_data) == 0) {
                    if (fields.name != NULL && fields.name_len > 0) {
                        int nlen = fields.name_len < 31 ? fields.name_len : 31;
                        memcpy(r->name, fields.name, nlen);
                        r->name[nlen] = '\0';
                        r->has_name = true;
                    }
                }
                scan_count++;
            }
            break;

        case BLE_GAP_EVENT_DISC_COMPLETE:
            log_debug("ble", "scan complete — %d devices found", scan_count);
            break;

        default:
            break;
    }
    return 0;
}

// ============================================================
// NimBLE host task
// ============================================================

static void nimble_host_task(void *param)
{
    nimble_port_run();
    nimble_port_freertos_deinit();
}

static void ble_on_sync(void)
{
    // Use best address available
    ble_hs_util_ensure_addr(0);
    log_info("ble", "stack ready");
}

static void ble_init_stack(void)
{
    if (ble_initialized) return;

    nimble_port_init();
    ble_hs_cfg.sync_cb = ble_on_sync;
    ble_svc_gap_init();
    ble_svc_gatt_init();

    nimble_port_freertos_init(nimble_host_task);
    ble_initialized = true;
}

// ============================================================
// Public API
// ============================================================

volt_error_t ble_advertise(const char *name)
{
    ble_init_stack();

    ble_svc_gap_device_name_set(name);

    struct ble_gap_adv_params adv_params = {
        .conn_mode = BLE_GAP_CONN_MODE_UND,
        .disc_mode = BLE_GAP_DISC_MODE_GEN,
    };

    struct ble_hs_adv_fields fields = {0};
    fields.flags = BLE_HS_ADV_F_DISC_GEN | BLE_HS_ADV_F_BREDR_UNSUP;
    fields.name = (uint8_t *)name;
    fields.name_len = strlen(name);
    fields.name_is_complete = 1;

    ble_gap_adv_set_fields(&fields);
    int rc = ble_gap_adv_start(BLE_OWN_ADDR_PUBLIC, NULL, BLE_HS_FOREVER,
                               &adv_params, gap_event_cb, NULL);
    if (rc != 0) {
        log_error("ble", "advertise failed (rc=%d)", rc);
        return VOLT_ERR_NOT_SUPPORTED;
    }

    log_info("ble", "advertising as \"%s\"", name);
    return VOLT_OK;
}

volt_error_t ble_stop(void)
{
    ble_gap_adv_stop();
    if (ble_connected) {
        ble_gap_terminate(conn_handle, BLE_ERR_REM_USER_CONN_TERM);
        ble_connected = false;
    }
    return VOLT_OK;
}

bool ble_is_connected(void)
{
    return ble_connected;
}

int ble_expose_int(const char *name, int initial_value)
{
    if (value_count >= BLE_MAX_VALUES) return -1;
    int idx = value_count++;
    ble_value_t *v = &values[idx];
    strncpy(v->name, name, 15);
    v->name[15] = '\0';
    v->type = VAL_INT;
    v->data.i = initial_value;
    v->active = true;
    return idx;
}

int ble_expose_float(const char *name, float initial_value)
{
    if (value_count >= BLE_MAX_VALUES) return -1;
    int idx = value_count++;
    ble_value_t *v = &values[idx];
    strncpy(v->name, name, 15);
    v->name[15] = '\0';
    v->type = VAL_FLOAT;
    v->data.f = initial_value;
    v->active = true;
    return idx;
}

int ble_expose_string(const char *name, const char *initial_value)
{
    if (value_count >= BLE_MAX_VALUES) return -1;
    int idx = value_count++;
    ble_value_t *v = &values[idx];
    strncpy(v->name, name, 15);
    v->name[15] = '\0';
    v->type = VAL_STRING;
    strncpy(v->data.s, initial_value, 20);
    v->data.s[20] = '\0';
    v->active = true;
    return idx;
}

volt_error_t ble_update_int(int value_id, int value)
{
    if (value_id < 0 || value_id >= BLE_MAX_VALUES) return VOLT_ERR_INVALID_ARG;
    if (!values[value_id].active) return VOLT_ERR_INVALID_ARG;
    values[value_id].data.i = value;
    return VOLT_OK;
}

volt_error_t ble_update_float(int value_id, float value)
{
    if (value_id < 0 || value_id >= BLE_MAX_VALUES) return VOLT_ERR_INVALID_ARG;
    if (!values[value_id].active) return VOLT_ERR_INVALID_ARG;
    values[value_id].data.f = value;
    return VOLT_OK;
}

volt_error_t ble_update_string(int value_id, const char *value)
{
    if (value_id < 0 || value_id >= BLE_MAX_VALUES) return VOLT_ERR_INVALID_ARG;
    if (!values[value_id].active) return VOLT_ERR_INVALID_ARG;
    strncpy(values[value_id].data.s, value, 20);
    values[value_id].data.s[20] = '\0';
    return VOLT_OK;
}

void ble_on_write(ble_write_callback_t callback)
{
    write_cb = callback;
}

int ble_scan(uint32_t duration_ms)
{
    ble_init_stack();
    scan_count = 0;

    struct ble_gap_disc_params params = {
        .passive = 1,
        .filter_duplicates = 1,
    };

    ble_gap_disc(BLE_OWN_ADDR_PUBLIC, duration_ms, &params, gap_event_cb, NULL);

    // Block until scan completes
    vTaskDelay(pdMS_TO_TICKS(duration_ms + 100));
    return scan_count;
}

const char *ble_scan_name(int index)
{
    if (index < 0 || index >= scan_count) return NULL;
    return scan_results[index].has_name ? scan_results[index].name : NULL;
}

int ble_scan_rssi(int index)
{
    if (index < 0 || index >= scan_count) return 0;
    return scan_results[index].rssi;
}
