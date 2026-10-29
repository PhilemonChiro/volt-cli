// A MCP4725 12-bit DAC: ramp the output from 0 V to 3.3 V and back to 0.
hardware {
    device dac: MCP4725
}

var v: float = 0.0

every 100ms {
    dac.set_voltage(v, 3.3)
    v += 0.1
    if v > 3.3 {
        v = 0.0
    }
}
