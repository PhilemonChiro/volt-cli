// MAX30102: Pulse oximeter and heart rate (18-bit) — logs its readings every 2 seconds.
hardware {
    device max30102: MAX30102
}

every 2s {
    let red: int = max30102.red()
    let ir: int = max30102.ir()
    let temperature: float = max30102.temperature()
    let is_finger_present: int = max30102.is_finger_present()
    log("max30102", "red={red} ir={ir} temperature={temperature} is_finger_present={is_finger_present}")
}
