// A DS18B20 probe on GPIO 4 (4.7k pull-up to 3.3V): log the temperature,
// and say so when the sensor stops answering.
hardware {
    device probe: DS18B20(pin: 4)
}

every 2s {
    let t: float = probe.temperature()
    if probe.is_connected() {
        log("probe", "temp={t} C")
    } else {
        log("probe", "sensor not answering")
    }
}
