// AHT20: Temperature and humidity — logs its readings every 2 seconds.
hardware {
    device aht20: AHT20
}

every 2s {
    let temperature: float = aht20.temperature()
    let humidity: float = aht20.humidity()
    log("aht20", "temperature={temperature} humidity={humidity}")
}
