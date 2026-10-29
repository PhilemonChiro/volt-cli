// MS5611: High-resolution barometric pressure — logs its readings every 2 seconds.
hardware {
    device ms5611: MS5611
}

every 2s {
    let temperature: float = ms5611.temperature()
    let pressure: float = ms5611.pressure()
    log("ms5611", "temperature={temperature} pressure={pressure}")
}
