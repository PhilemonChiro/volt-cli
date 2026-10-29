// SHT31: High-accuracy temperature and humidity — logs its readings every 2 seconds.
hardware {
    device sht31: SHT31
}

every 2s {
    let temperature: float = sht31.temperature()
    let humidity: float = sht31.humidity()
    log("sht31", "temperature={temperature} humidity={humidity}")
}
