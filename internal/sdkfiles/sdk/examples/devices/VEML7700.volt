// VEML7700: High-accuracy ambient light sensor — logs its readings every 2 seconds.
hardware {
    device veml7700: VEML7700
}

every 2s {
    let lux: float = veml7700.lux()
    let white: float = veml7700.white()
    log("veml7700", "lux={lux} white={white}")
}
