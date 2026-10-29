// MAX31855: Thermocouple-to-digital converter — logs its readings every 2 seconds.
hardware {
    device max31855: MAX31855(cs: 4)
}

every 2s {
    let temperature: float = max31855.temperature()
    let internal: float = max31855.internal()
    let is_fault: int = max31855.is_fault()
    log("max31855", "temperature={temperature} internal={internal} is_fault={is_fault}")
}
