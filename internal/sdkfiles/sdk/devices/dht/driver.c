#include "volt/gpio_devices.h"
/* DHT11/DHT22 Temperature + Humidity (single-wire GPIO protocol)
   Not I2C/SPI — uses timed GPIO pulses */

#include "volt/pin.h"
#include "volt/log.h"
#include "volt/task.h"
#include <stdint.h>

volt_error_t dht_init(dht_t *dev, uint8_t gpio, uint8_t type)
{
    dev->gpio = gpio;
    dev->type = (type == 11) ? 11 : 22;
    dev->temperature = 0;
    dev->humidity = 0;
    pin_set_input(gpio);
    log_info("dht", "DHT%d on GPIO %d", dev->type, gpio);
    return VOLT_OK;
}

static int dht_read_raw(dht_t *dev, uint8_t data[5])
{
    uint8_t gpio = dev->gpio;

    // Send start signal: pull low 20ms, then release high.
    pin_set_output(gpio);            // output
    pin_write(gpio, 0);
    volt_delay_ms(20);
    pin_write(gpio, 1);

    // Switch to input and wait for the response (all loops iteration-bounded).
    pin_set_input(gpio);             // input

    int timeout = 10000;
    while (pin_read(gpio) == 1 && --timeout > 0);
    if (timeout == 0) return -1;
    while (pin_read(gpio) == 0 && --timeout > 0);
    if (timeout == 0) return -1;
    while (pin_read(gpio) == 1 && --timeout > 0);
    if (timeout == 0) return -1;

    // Read 40 bits (5 bytes)
    for (int i = 0; i < 40; i++) {
        while (pin_read(gpio) == 0 && --timeout > 0);
        int count = 0;
        while (pin_read(gpio) == 1 && --timeout > 0) count++;
        data[i / 8] <<= 1;
        if (count > 40) data[i / 8] |= 1;  // >40 cycles = bit 1
    }

    // Verify checksum
    uint8_t sum = data[0] + data[1] + data[2] + data[3];
    if (sum != data[4]) return -2;
    return 0;
}

float dht_temperature(dht_t *dev)
{
    uint8_t data[5] = {0};
    if (dht_read_raw(dev, data) != 0) return dev->temperature;

    if (dev->type == 22) {
        int16_t t = ((int16_t)(data[2] & 0x7F) << 8) | data[3];
        if (data[2] & 0x80) t = -t;
        dev->temperature = t / 10.0f;
    } else {
        dev->temperature = (float)data[2] + data[3] / 10.0f;
    }
    dev->humidity = (dev->type == 22)
        ? (((uint16_t)data[0] << 8) | data[1]) / 10.0f
        : (float)data[0] + data[1] / 10.0f;

    return dev->temperature;
}

float dht_humidity(dht_t *dev)
{
    dht_temperature(dev);  // reads both
    return dev->humidity;
}
