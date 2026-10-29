// A TCA9548A I2C multiplexer: switch between channels so two sensors with the
// same address can share one bus.
hardware {
    device mux: TCA9548A
}

var ch: int = 0

every 1s {
    mux.select(ch)
    log("mux", "channel {ch} selected")
    ch = (ch + 1) % 8
}
