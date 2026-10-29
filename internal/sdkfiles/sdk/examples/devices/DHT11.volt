// DHT11: Basic temperature and humidity — logs its readings every 2 seconds.
hardware {
    device dht11: DHT11(pin: 4)
}

every 2s {
    let humidity: float = dht11.humidity()
    let temperature: float = dht11.temperature()
    log("dht11", "humidity={humidity} temperature={temperature}")
}
