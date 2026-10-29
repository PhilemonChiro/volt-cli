// ITG3205: 3-axis gyroscope (found on the GY-85 9-DoF board) — logs its readings every 2 seconds.
hardware {
    device itg3205: ITG3205
}

every 2s {
    let x: float = itg3205.x()
    let y: float = itg3205.y()
    let z: float = itg3205.z()
    let magnitude: float = itg3205.magnitude()
    log("itg3205", "x={x} y={y} z={z} magnitude={magnitude}")
}
