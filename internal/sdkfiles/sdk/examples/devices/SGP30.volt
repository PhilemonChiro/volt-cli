// SGP30: TVOC and eCO2 once per second, with the baseline saved every hour
// and restored at boot so the readings don't need days to settle again.
hardware {
    device sgp30: SGP30
}

storage base_eco2: int = 0

storage base_tvoc: int = 0

setup {
    sgp30.set_baseline(base_eco2, base_tvoc)
}

every 1s {
    let eco2: int = sgp30.eco2()
    let tvoc: int = sgp30.tvoc()
    if sgp30.ready() {
        log("sgp30", "eco2={eco2}ppm tvoc={tvoc}ppb")
    } else {
        log("sgp30", "warming up")
    }
}

every 1hour {
    base_eco2 = sgp30.baseline_eco2()
    base_tvoc = sgp30.baseline_tvoc()
    storage.save("base_eco2", base_eco2)
    storage.save("base_tvoc", base_tvoc)
}
