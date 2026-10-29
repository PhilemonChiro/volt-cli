// BME680: Gas, temperature, humidity, pressure — logs its readings every 2 seconds.
hardware {
    device bme680: BME680
}

every 2s {
    let temperature: float = bme680.temperature()
    let humidity: float = bme680.humidity()
    let pressure: float = bme680.pressure()
    let gas: int = bme680.gas()
    log("bme680", "temperature={temperature} humidity={humidity} pressure={pressure} gas={gas}")
}
