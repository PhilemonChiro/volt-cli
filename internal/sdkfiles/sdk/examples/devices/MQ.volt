// MQ-2 gas sensor on an analog pin: calibrate R0 once in clean air, keep it
// across reboots, then log LPG and smoke estimates every 2 seconds.
// AO -> 10k -> GPIO34 -> 20k -> GND (divider 1.5). Module RL is 1k (marked 102).
// For other MQ models swap the clean-air ratio and curve constants (see device tips).
hardware {
    device mq2: MQ(pin: 34)
}

storage r0: float = 0.0

setup {
    mq2.set_load(1.0)
    mq2.set_divider(1.5)
    if r0 > 0.0 {
        mq2.set_r0(r0)
    } else {
        sleep(60s)
        r0 = mq2.calibrate(9.83)
        storage.save("r0", r0)
    }
    log("mq2", "R0={r0} kOhm")
}

every 2s {
    let ratio: float = mq2.ratio()
    let lpg: float = mq2.ppm(574.25, -2.222)
    let smoke: float = mq2.ppm(3616.1, -2.675)
    log("mq2", "Rs/R0={ratio} lpg={lpg}ppm smoke={smoke}ppm")
    if lpg > 1000.0 {
        log("mq2", "GAS ALARM")
    }
}
