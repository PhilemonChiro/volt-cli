// INA219: Voltage, current, and power monitor — logs its readings every 2 seconds.
hardware {
    device ina219: INA219
}

every 2s {
    let voltage: float = ina219.voltage()
    let current: float = ina219.current()
    let power: float = ina219.power()
    let shunt_voltage: float = ina219.shunt_voltage()
    log("ina219", "voltage={voltage} current={current} power={power} shunt_voltage={shunt_voltage}")
}
