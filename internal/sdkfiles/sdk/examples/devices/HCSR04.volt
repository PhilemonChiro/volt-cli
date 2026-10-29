// HCSR04: Ultrasonic distance sensor (2-400cm) — logs its readings every 2 seconds.
hardware {
    device hcsr04: HCSR04(trig: 4, echo: 5)
}

every 2s {
    let distance_cm: float = hcsr04.distance_cm()
    log("hcsr04", "distance_cm={distance_cm}")
}
