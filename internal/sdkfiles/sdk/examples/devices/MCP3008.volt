// MCP3008: 8-channel 10-bit SPI ADC (CS on GPIO 4) — read channels 0 and 1.
hardware {
    device adc: MCP3008(cs: 4)
}

every 500ms {
    let ch0: int = adc.read(0)
    let ch1: int = adc.read(1)
    log("adc", "ch0={ch0}  ch1={ch1}  (0-1023)")
}
