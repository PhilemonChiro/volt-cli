// BMP180: Barometric pressure and temperature — logs its readings every 2 seconds.
hardware {
    device bmp180: BMP180
}

every 2s {
    let temperature: float = bmp180.temperature()
    let pressure: float = bmp180.pressure()
    log("bmp180", "temperature={temperature} pressure={pressure}")
}
