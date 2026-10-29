// BME280: Temperature, humidity, pressure — logs its readings every 2 seconds.
hardware {
    device bme280: BME280
}

every 2s {
    let temperature: float = bme280.temperature()
    let humidity: float = bme280.humidity()
    let pressure: float = bme280.pressure()
    log("bme280", "temperature={temperature} humidity={humidity} pressure={pressure}")
}
