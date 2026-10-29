// MAX30100: Pulse oximeter and heart rate (16-bit) — logs its readings every 2 seconds.
hardware {
    device max30100: MAX30100
}

every 2s {
    let red: int = max30100.red()
    let ir: int = max30100.ir()
    let temperature: float = max30100.temperature()
    let is_finger_present: int = max30100.is_finger_present()
    log("max30100", "red={red} ir={ir} temperature={temperature} is_finger_present={is_finger_present}")
}
