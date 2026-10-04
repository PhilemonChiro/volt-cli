/* MLX90614 Non-contact IR Temperature Sensor (I2C addr: 0x5A) */

#include "volt/sensor.h"
#include "volt/i2c.h"
#include "volt/log.h"

#define MLX_REG_AMBIENT  0x06
#define MLX_REG_OBJECT   0x07

volt_error_t mlx90614_init(mlx90614_t *dev, uint8_t bus_id, uint8_t addr)
{
    dev->bus_id = bus_id;
    dev->addr = addr ? addr : 0x5A;
    log_info("mlx90614", "IR thermometer at 0x%02X", dev->addr);
    return VOLT_OK;
}

static float mlx_read_temp(mlx90614_t *dev, uint8_t reg)
{
    uint8_t data[3]; // LSB, MSB, PEC (CRC)
    volt_i2c_write_read(dev->bus_id, dev->addr, &reg, 1, data, 3);
    uint16_t raw = (data[1] << 8) | data[0];
    // Convert to °C: temp = raw * 0.02 - 273.15
    return raw * 0.02f - 273.15f;
}

float mlx90614_ambient(mlx90614_t *dev) { return mlx_read_temp(dev, MLX_REG_AMBIENT); }
float mlx90614_object(mlx90614_t *dev)  { return mlx_read_temp(dev, MLX_REG_OBJECT); }
