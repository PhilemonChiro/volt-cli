#include "volt/i2c.h"
#include "volt/log.h"
#include "driver/i2c.h"
#include "driver/gpio.h"
#include "esp_rom_sys.h"        // esp_rom_delay_us

#define I2C_TIMEOUT_MS 1000

// ---- Soft (bit-banged) I2C — bus ids >= VOLT_SOFT_I2C_BASE route here. Lets any
// GPIO pair be an I2C bus (unlimited buses / same-address devices on separate
// buses). Open-drain: drive low = output 0; "high" = release to input and let
// the pull-up raise the line.
#define VOLT_SOFT_I2C_BASE 100
#define VOLT_SOFT_I2C_MAX  8

typedef struct { uint8_t sda, scl; } soft_i2c_t;
static soft_i2c_t g_soft[VOLT_SOFT_I2C_MAX];

static inline soft_i2c_t *soft_bus(uint8_t id) {
    uint8_t i = (uint8_t)(id - VOLT_SOFT_I2C_BASE);
    return (i < VOLT_SOFT_I2C_MAX) ? &g_soft[i] : NULL;
}
static inline void si_delay(void)          { esp_rom_delay_us(5); }   // ~100 kHz
static inline void sda_hi(soft_i2c_t *b)   { gpio_set_direction(b->sda, GPIO_MODE_INPUT); }
static inline void sda_lo(soft_i2c_t *b)   { gpio_set_direction(b->sda, GPIO_MODE_OUTPUT); gpio_set_level(b->sda, 0); }
static inline void scl_hi(soft_i2c_t *b)   { gpio_set_direction(b->scl, GPIO_MODE_INPUT); }
static inline void scl_lo(soft_i2c_t *b)   { gpio_set_direction(b->scl, GPIO_MODE_OUTPUT); gpio_set_level(b->scl, 0); }
static inline int  sda_rd(soft_i2c_t *b)   { return gpio_get_level(b->sda); }

static void si_start(soft_i2c_t *b) { sda_hi(b); scl_hi(b); si_delay(); sda_lo(b); si_delay(); scl_lo(b); }
static void si_stop(soft_i2c_t *b)  { sda_lo(b); si_delay(); scl_hi(b); si_delay(); sda_hi(b); si_delay(); }

static int si_write(soft_i2c_t *b, uint8_t v) {          // returns 1 if ACKed
    for (int i = 0; i < 8; i++) {
        (v & 0x80) ? sda_hi(b) : sda_lo(b);
        v <<= 1;
        si_delay(); scl_hi(b); si_delay(); scl_lo(b);
    }
    sda_hi(b); si_delay(); scl_hi(b); si_delay();        // release SDA, clock the ACK bit
    int ack = (sda_rd(b) == 0);
    scl_lo(b);
    return ack;
}
static uint8_t si_read(soft_i2c_t *b, int ack) {
    uint8_t v = 0;
    sda_hi(b);
    for (int i = 0; i < 8; i++) {
        v <<= 1;
        scl_hi(b); si_delay();
        if (sda_rd(b)) v |= 1;
        scl_lo(b); si_delay();
    }
    ack ? sda_lo(b) : sda_hi(b);
    si_delay(); scl_hi(b); si_delay(); scl_lo(b);
    sda_hi(b);
    return v;
}

volt_error_t volt_i2c_init(uint8_t bus_id, uint8_t sda, uint8_t scl, uint32_t speed)
{
    if (bus_id >= VOLT_SOFT_I2C_BASE) {
        soft_i2c_t *b = soft_bus(bus_id);
        if (!b) { log_error("i2c", "soft bus %d out of range", bus_id); return VOLT_ERR_INTERNAL; }
        b->sda = sda; b->scl = scl;
        gpio_reset_pin(sda); gpio_reset_pin(scl);
        gpio_set_pull_mode(sda, GPIO_PULLUP_ONLY);
        gpio_set_pull_mode(scl, GPIO_PULLUP_ONLY);
        sda_hi(b); scl_hi(b);
        (void)speed;
        log_info("i2c", "soft bus %d initialized (SDA=%d SCL=%d, bit-banged)", bus_id, sda, scl);
        return VOLT_OK;
    }

    i2c_config_t conf = {
        .mode = I2C_MODE_MASTER,
        .sda_io_num = sda,
        .scl_io_num = scl,
        .sda_pullup_en = GPIO_PULLUP_ENABLE,
        .scl_pullup_en = GPIO_PULLUP_ENABLE,
        .master.clk_speed = speed ? speed : 400000,
    };

    esp_err_t err = i2c_param_config(bus_id, &conf);
    if (err != ESP_OK) {
        log_error("i2c", "config failed on bus %d (err=%d)", bus_id, err);
        return VOLT_ERR_INTERNAL;
    }

    err = i2c_driver_install(bus_id, I2C_MODE_MASTER, 0, 0, 0);
    if (err != ESP_OK) {
        log_error("i2c", "driver install failed on bus %d (err=%d)", bus_id, err);
        return VOLT_ERR_INTERNAL;
    }

    log_info("i2c", "bus %d initialized (SDA=%d SCL=%d speed=%lu)",
             bus_id, sda, scl, (unsigned long)speed);
    return VOLT_OK;
}

volt_error_t volt_i2c_write(uint8_t bus_id, uint8_t addr, const uint8_t *data, uint8_t len)
{
    if (bus_id >= VOLT_SOFT_I2C_BASE) {
        soft_i2c_t *b = soft_bus(bus_id);
        if (!b) return VOLT_ERR_INTERNAL;
        si_start(b);
        if (!si_write(b, (uint8_t)(addr << 1))) { si_stop(b); return VOLT_ERR_INTERNAL; }
        for (uint8_t i = 0; i < len; i++) {
            if (!si_write(b, data[i])) { si_stop(b); return VOLT_ERR_INTERNAL; }
        }
        si_stop(b);
        return VOLT_OK;
    }

    i2c_cmd_handle_t cmd = i2c_cmd_link_create();
    i2c_master_start(cmd);
    i2c_master_write_byte(cmd, (addr << 1) | I2C_MASTER_WRITE, true);
    i2c_master_write(cmd, data, len, true);
    i2c_master_stop(cmd);
    esp_err_t err = i2c_master_cmd_begin(bus_id, cmd, I2C_TIMEOUT_MS / portTICK_PERIOD_MS);
    i2c_cmd_link_delete(cmd);
    return err == ESP_OK ? VOLT_OK : VOLT_ERR_INTERNAL;
}

volt_error_t volt_i2c_read(uint8_t bus_id, uint8_t addr, uint8_t *buf, uint8_t len)
{
    if (bus_id >= VOLT_SOFT_I2C_BASE) {
        soft_i2c_t *b = soft_bus(bus_id);
        if (!b) return VOLT_ERR_INTERNAL;
        si_start(b);
        if (!si_write(b, (uint8_t)((addr << 1) | 1))) { si_stop(b); return VOLT_ERR_INTERNAL; }
        for (uint8_t i = 0; i < len; i++) buf[i] = si_read(b, i < (len - 1));
        si_stop(b);
        return VOLT_OK;
    }

    i2c_cmd_handle_t cmd = i2c_cmd_link_create();
    i2c_master_start(cmd);
    i2c_master_write_byte(cmd, (addr << 1) | I2C_MASTER_READ, true);
    if (len > 1) {
        i2c_master_read(cmd, buf, len - 1, I2C_MASTER_ACK);
    }
    i2c_master_read_byte(cmd, buf + len - 1, I2C_MASTER_NACK);
    i2c_master_stop(cmd);
    esp_err_t err = i2c_master_cmd_begin(bus_id, cmd, I2C_TIMEOUT_MS / portTICK_PERIOD_MS);
    i2c_cmd_link_delete(cmd);
    return err == ESP_OK ? VOLT_OK : VOLT_ERR_INTERNAL;
}

volt_error_t volt_i2c_write_read(uint8_t bus_id, uint8_t addr,
                            const uint8_t *write_data, uint8_t write_len,
                            uint8_t *read_buf, uint8_t read_len)
{
    if (bus_id >= VOLT_SOFT_I2C_BASE) {
        soft_i2c_t *b = soft_bus(bus_id);
        if (!b) return VOLT_ERR_INTERNAL;
        si_start(b);
        if (!si_write(b, (uint8_t)(addr << 1))) { si_stop(b); return VOLT_ERR_INTERNAL; }
        for (uint8_t i = 0; i < write_len; i++) {
            if (!si_write(b, write_data[i])) { si_stop(b); return VOLT_ERR_INTERNAL; }
        }
        si_start(b);   // repeated start
        if (!si_write(b, (uint8_t)((addr << 1) | 1))) { si_stop(b); return VOLT_ERR_INTERNAL; }
        for (uint8_t i = 0; i < read_len; i++) read_buf[i] = si_read(b, i < (read_len - 1));
        si_stop(b);
        return VOLT_OK;
    }

    i2c_cmd_handle_t cmd = i2c_cmd_link_create();
    // Write phase
    i2c_master_start(cmd);
    i2c_master_write_byte(cmd, (addr << 1) | I2C_MASTER_WRITE, true);
    i2c_master_write(cmd, write_data, write_len, true);
    // Repeated start + read phase
    i2c_master_start(cmd);
    i2c_master_write_byte(cmd, (addr << 1) | I2C_MASTER_READ, true);
    if (read_len > 1) {
        i2c_master_read(cmd, read_buf, read_len - 1, I2C_MASTER_ACK);
    }
    i2c_master_read_byte(cmd, read_buf + read_len - 1, I2C_MASTER_NACK);
    i2c_master_stop(cmd);
    esp_err_t err = i2c_master_cmd_begin(bus_id, cmd, I2C_TIMEOUT_MS / portTICK_PERIOD_MS);
    i2c_cmd_link_delete(cmd);
    return err == ESP_OK ? VOLT_OK : VOLT_ERR_INTERNAL;
}

uint8_t volt_i2c_read_reg(uint8_t bus_id, uint8_t addr, uint8_t reg)
{
    uint8_t val = 0;
    volt_i2c_write_read(bus_id, addr, &reg, 1, &val, 1);
    return val;
}

volt_error_t volt_i2c_write_reg(uint8_t bus_id, uint8_t addr, uint8_t reg, uint8_t value)
{
    uint8_t data[2] = { reg, value };
    return volt_i2c_write(bus_id, addr, data, 2);
}

uint16_t volt_i2c_read_reg16(uint8_t bus_id, uint8_t addr, uint8_t reg)
{
    uint8_t buf[2] = {0};
    volt_i2c_write_read(bus_id, addr, &reg, 1, buf, 2);
    return (buf[0] << 8) | buf[1];
}
