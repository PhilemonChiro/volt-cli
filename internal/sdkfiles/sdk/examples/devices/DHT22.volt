// DHT22: Temperature and humidity (AM2302, higher precision than DHT11) — logs its readings every 2 seconds.
hardware {
    device dht22: DHT22(pin: 4)
}

every 2s {
    let temperature: float = dht22.temperature()
    let humidity: float = dht22.humidity()
    log("dht22", "temperature={temperature} humidity={humidity}")
}
