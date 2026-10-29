// ADXL345: 3-axis accelerometer (found on the GY-85 9-DoF board) — logs its readings every 2 seconds.
hardware {
    device adxl345: ADXL345
}

every 2s {
    let x: float = adxl345.x()
    let y: float = adxl345.y()
    let z: float = adxl345.z()
    let magnitude: float = adxl345.magnitude()
    log("adxl345", "x={x} y={y} z={z} magnitude={magnitude}")
}
