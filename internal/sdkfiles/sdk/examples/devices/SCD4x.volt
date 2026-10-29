// SCD40/SCD41: true CO2, temperature and humidity every 5 seconds (the sensor's
// own rate), with a ventilation warning above 1000 ppm.
hardware {
    device scd4x: SCD4x
}

setup {
    scd4x.set_temperature_offset(4.0)
}

every 5s {
    if scd4x.ready() {
        let co2: int = scd4x.co2()
        let temperature: float = scd4x.temperature()
        let humidity: float = scd4x.humidity()
        log("scd4x", "co2={co2}ppm temperature={temperature} humidity={humidity}")
        if co2 > 1000 {
            log("scd4x", "ventilate")
        }
    }
}
