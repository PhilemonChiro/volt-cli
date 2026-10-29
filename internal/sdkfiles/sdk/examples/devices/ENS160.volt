// ENS160 + AHT21 combo board: air quality index, TVOC and eCO2 every 2 seconds,
// compensated with the AHT21's temperature and humidity (same I2C bus).
hardware {
    device ens160: ENS160
    device aht: AHT20
}

every 2s {
    ens160.set_compensation(aht.temperature(), aht.humidity())
    let status: int = ens160.status()
    let aqi: int = ens160.aqi()
    let tvoc: int = ens160.tvoc()
    let eco2: int = ens160.eco2()
    match(status) {
        0 -> log("ens160", "aqi={aqi} tvoc={tvoc}ppb eco2={eco2}ppm")
        1 -> log("ens160", "warming up")
        2 -> log("ens160", "initial start-up (first 24 h): aqi={aqi}")
        _ -> log("ens160", "no valid reading (status={status})")
    }
}
