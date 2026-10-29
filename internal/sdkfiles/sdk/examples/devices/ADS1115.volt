// ADS1115: 16-bit 4-channel ADC — read a potentiometer or sensor on A0 and A1.
hardware {
    device adc: ADS1115
}

every 500ms {
    let a0: float = adc.voltage(0)
    let a1: float = adc.voltage(1)
    let raw: int = adc.read_raw(0)
    log("adc", "A0={a0} V  A1={a1} V  (A0 raw={raw})")
}
