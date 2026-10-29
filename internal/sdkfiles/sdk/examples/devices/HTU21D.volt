// HTU21D: Temperature and humidity — logs its readings every 2 seconds.
hardware {
    device htu21d: HTU21D
}

every 2s {
    let temperature: float = htu21d.temperature()
    let humidity: float = htu21d.humidity()
    log("htu21d", "temperature={temperature} humidity={humidity}")
}
