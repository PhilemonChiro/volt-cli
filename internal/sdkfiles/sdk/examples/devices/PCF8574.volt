// A PCF8574 I2C GPIO expander: blink output P0 and log the inputs.
hardware {
    device io: PCF8574
}

var level: int = 0

every 500ms {
    level = 1 - level
    io.set_pin(0, level)
    let pins: int = io.read()
    log("io", "pins={pins}")
}
